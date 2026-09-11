package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/plugin/sdk/hosthttp"
	"google.golang.org/protobuf/encoding/protowire"
)

var docPath = regexp.MustCompile(`^/doc/([A-Za-z0-9_-]+)/*$`)

type document struct{ ID, URL, Title, Text string }
type config struct {
	ResourceIDs []string `json:"resource_ids"`
	Settings    struct {
		URL string `json:"url"`
	} `json:"settings"`
}

func parseConfig(raw []byte) (config, []document, error) {
	var c config
	if err := json.Unmarshal(raw, &c); err != nil {
		return c, nil, fmt.Errorf("无效数据源配置")
	}
	parts := strings.FieldsFunc(c.Settings.URL, func(r rune) bool { return r == ',' || r == '，' || r == '\n' || r == '\r' || r == '\t' || r == ' ' })
	if len(parts) == 0 || len(parts) > 20 {
		return c, nil, fmt.Errorf("请填写 1 至 20 个腾讯文档链接，用逗号或换行分隔")
	}
	seen := map[string]bool{}
	docs := []document{}
	for _, rawURL := range parts {
		u, err := url.Parse(rawURL)
		if err != nil || u.Scheme != "https" || u.User != nil || !strings.EqualFold(u.Hostname(), "docs.qq.com") || (u.Port() != "" && u.Port() != "443") {
			return c, nil, fmt.Errorf("仅支持 https://docs.qq.com/doc/文档ID 的公开文档链接")
		}
		match := docPath.FindStringSubmatch(u.Path)
		if match == nil {
			return c, nil, fmt.Errorf("请填写具体文档 /doc/ 链接；官网首页、表格及文件夹不能作为文档导入")
		}
		id := match[1]
		if !seen[id] {
			docs = append(docs, document{ID: id, URL: "https://docs.qq.com/doc/" + id})
			seen[id] = true
		}
	}
	return c, docs, nil
}

// Read the anonymous document snapshot, never the homepage or HTML page shell.
func (s *server) read(ctx context.Context, doc document) (document, error) {
	query := url.Values{"id": {doc.ID}, "normal": {"1"}, "outformat": {"1"}, "noEscape": {"1"}, "commandsFormat": {"1"}}
	resp, err := s.http.Do(ctx, hosthttp.Request{URL: "https://docs.qq.com/dop-api/opendoc?" + query.Encode(), Method: http.MethodGet, Headers: http.Header{"User-Agent": {"Mozilla/5.0"}, "Referer": {"https://docs.qq.com/"}, "Accept": {"application/json"}}})
	if err != nil {
		return doc, fmt.Errorf("腾讯文档受控读取失败：%w", err)
	}
	if resp.StatusCode != 200 {
		return doc, fmt.Errorf("腾讯文档返回 HTTP %d；请确认链接允许任何人查看", resp.StatusCode)
	}
	return decodeDocument(resp.Body, doc)
}

func decodeDocument(raw []byte, doc document) (document, error) {
	var data struct {
		Ret     int    `json:"ret"`
		PadType string `json:"padType"`
		Client  struct {
			Title     string         `json:"title"`
			Privilege map[string]int `json:"privilegeAttribute"`
			Collab    struct {
				Chunked bool   `json:"isChunked"`
				PadType string `json:"padType"`
				Initial struct {
					Text []string `json:"text"`
				} `json:"initialAttributedText"`
			} `json:"collab_client_vars"`
		} `json:"clientVars"`
	}
	if json.Unmarshal(raw, &data) != nil {
		return doc, fmt.Errorf("腾讯文档没有返回可解析的正文数据；可能需要登录或接口格式已变化")
	}
	if data.Ret != 0 {
		return doc, fmt.Errorf("腾讯文档拒绝读取（错误码 %d），请检查公开访问权限", data.Ret)
	}
	if canRead, ok := data.Client.Privilege["can_read"]; ok && canRead == 0 {
		return doc, fmt.Errorf("腾讯文档不允许匿名读取")
	}
	if data.PadType != "doc" || data.Client.Title == "" {
		return doc, fmt.Errorf("未获取到普通文档信息，请检查链接类型和公开权限")
	}
	if data.Client.Collab.Chunked {
		return doc, fmt.Errorf("该文档采用分块加载，当前版本无法保证完整读取，请拆分为较小文档")
	}
	if len(data.Client.Collab.Initial.Text) != 1 {
		return doc, fmt.Errorf("不支持的腾讯文档快照格式，已停止导入以避免遗漏正文")
	}
	wire, err := base64.StdEncoding.DecodeString(data.Client.Collab.Initial.Text[0])
	if err != nil {
		return doc, fmt.Errorf("腾讯文档正文编码无效")
	}
	body, err := snapshotText(wire)
	if err != nil {
		return doc, err
	}
	doc.Title, doc.Text = data.Client.Title, body
	return doc, nil
}

// Snapshot v3: repeated document(1) -> command(2); insert command type(1)=1,
// text insertion(6) -> UTF-8 text(1). Do not scrape arbitrary protobuf strings:
// that would mix style names, author identifiers and URLs into the document.
func fields(raw []byte, wanted protowire.Number) ([][]byte, error) {
	var values [][]byte
	for len(raw) > 0 {
		n, t, k := protowire.ConsumeTag(raw)
		if k < 0 {
			return nil, fmt.Errorf("无效文档快照")
		}
		raw = raw[k:]
		size := protowire.ConsumeFieldValue(n, t, raw)
		if size < 0 {
			return nil, fmt.Errorf("不完整的文档快照")
		}
		if n == wanted && t == protowire.BytesType {
			v, k := protowire.ConsumeBytes(raw)
			if k < 0 {
				return nil, fmt.Errorf("无效文本字段")
			}
			values = append(values, v)
		}
		raw = raw[size:]
	}
	return values, nil
}

func snapshotText(raw []byte) (string, error) {
	documents, err := fields(raw, 1)
	if err != nil {
		return "", err
	}
	var texts []string
	for _, d := range documents {
		commands, err := fields(d, 2)
		if err != nil {
			return "", err
		}
		for _, c := range commands {
			// Only the snapshot's insert command contains field 6.
			inserts, err := fields(c, 6)
			if err != nil {
				return "", err
			}
			for _, insert := range inserts {
				bodies, err := fields(insert, 1)
				if err != nil {
					return "", err
				}
				for _, body := range bodies {
					if !utf8.Valid(body) {
						return "", fmt.Errorf("文档正文不是有效 UTF-8")
					}
					texts = append(texts, string(body))
				}
			}
		}
	}
	if len(texts) != 1 {
		return "", fmt.Errorf("快照没有单一完整正文，已停止导入以避免内容错序")
	}
	text := strings.Map(func(r rune) rune {
		switch r {
		case '\r':
			return '\n'
		case '\n', '\t':
			return r
		}
		if r < 32 {
			return -1
		}
		return r
	}, texts[0])
	text = strings.TrimSpace(text)
	if text == "" {
		return "", fmt.Errorf("文档正文为空；请添加文字后重试")
	}
	return text, nil
}
