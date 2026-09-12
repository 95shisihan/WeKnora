// A diagnostic variant of the public Feishu datasource for OS network-policy acceptance.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	pb "example.org/weknora-feishu-public-no-network/proto"
	"example.org/weknora-feishu-public-no-network/sdk/transport"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
)

const pluginID = "dev.example.feishu-public-no-network"
const connectorType = "feishu_public_no_network"

var hostPattern = regexp.MustCompile(`^[a-zA-Z0-9-]+\.feishu\.cn$`)
var pathPattern = regexp.MustCompile(`^/(article/)?(wiki|docx)/[A-Za-z0-9_-]+/?$`)

type server struct {
	pb.UnimplementedDatasourcePluginServer
	client *http.Client
}

func validURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || !hostPattern.MatchString(u.Hostname()) ||
		(u.Port() != "" && u.Port() != "443") || !pathPattern.MatchString(u.Path) {
		return nil, errors.New("仅支持 https://<租户>.feishu.cn/wiki/<token> 或 /docx/<token> 公开链接")
	}
	u.RawQuery, u.Fragment = "", ""
	return u, nil
}

func newClient() *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar, Timeout: 15 * time.Second, Transport: &http.Transport{Proxy: nil},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("too many redirects")
			}
			// Match the original public-link reader's anonymous guest redirect flow.
			u := req.URL
			if u.Scheme == "https" && u.User == nil && ((u.Host == "accounts.feishu.cn" && u.Path == "/accounts/page/login") || (u.Host == "login.feishu.cn" && u.Path == "/accounts/trap")) {
				return nil
			}
			_, err := validURL(req.URL.String())
			return err
		}}
}

func (s *server) GetInfo(context.Context, *pb.GetInfoRequest) (*pb.GetInfoResponse, error) {
	return &pb.GetInfoResponse{Id: pluginID, Version: "0.1.0", ProtocolVersion: "v1", ConnectorType: connectorType}, nil
}

func (s *server) Validate(ctx context.Context, req *pb.ConfigRequest) (*pb.Empty, error) {
	var config struct {
		Settings struct {
			URLs string `json:"public_urls"`
		} `json:"settings"`
	}
	if json.Unmarshal(req.GetConfigJson(), &config) != nil {
		return nil, status.Error(codes.InvalidArgument, "无效配置")
	}
	links := strings.FieldsFunc(config.Settings.URLs, func(r rune) bool { return r == ',' || r == '，' || r == '\n' || r == '\r' || r == ' ' || r == '\t' })
	if len(links) < 1 || len(links) > 20 {
		return nil, status.Error(codes.InvalidArgument, "请填写 1 至 20 个公开飞书链接")
	}
	// Validate every URL before issuing any request.
	urls := make([]*url.URL, 0, len(links))
	for _, link := range links {
		u, err := validURL(link)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		urls = append(urls, u)
	}
	for _, u := range urls {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, "无法创建请求")
		}
		request.Header.Set("User-Agent", "Mozilla/5.0")
		response, err := s.client.Do(request)
		if err != nil {
			// Preserve the actual OS error, but do not echo private document tokens.
			var ue *url.Error
			if errors.As(err, &ue) {
				err = ue.Err
			}
			return nil, status.Errorf(codes.Unavailable, "飞书公开链接网络请求失败（本插件声明禁止联网）：%v", err)
		}
		_, readErr := io.Copy(io.Discard, io.LimitReader(response.Body, 8192))
		response.Body.Close()
		if readErr != nil {
			return nil, status.Error(codes.Unavailable, "读取响应失败")
		}
		if response.StatusCode != http.StatusOK {
			return nil, status.Errorf(codes.FailedPrecondition, "服务器返回 HTTP %d", response.StatusCode)
		}
	}
	// Never fake a denial: success here signals that this request was not blocked.
	return &pb.Empty{}, nil
}

func (s *server) ListResources(context.Context, *pb.ListResourcesRequest) (*pb.ListResourcesResponse, error) {
	return nil, status.Error(codes.FailedPrecondition, "此插件仅用于禁网连接验证，不提供文档导入")
}

func main() {
	address := os.Getenv("WEKNORA_PLUGIN_ADDRESS")
	if address == "" {
		address = "stdio://"
	}
	l, err := transport.Listen(address)
	if err != nil {
		panic(err)
	}
	defer l.Close()
	g := grpc.NewServer()
	pb.RegisterDatasourcePluginServer(g, &server{client: newClient()})
	h := health.NewServer()
	h.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)
	grpc_health_v1.RegisterHealthServer(g, h)
	if err := g.Serve(l); err != nil {
		panic(err)
	}
}
