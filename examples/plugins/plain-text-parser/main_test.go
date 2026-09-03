package main

import (
	"testing"

	pluginproto "github.com/Tencent/WeKnora/plugin/proto"
)

func TestParsePlainText(t *testing.T) {
	result, err := parse(&pluginproto.DocumentParseRequest{FileType: "txt", FileContent: []byte("# Hello\n")})
	if err != nil {
		t.Fatal(err)
	}
	if result != "# Hello\n" {
		t.Fatalf("parse result %q", result)
	}
}

func TestParseRejectsUnsupportedType(t *testing.T) {
	if _, err := parse(&pluginproto.DocumentParseRequest{FileType: "pdf"}); err == nil {
		t.Fatal("expected unsupported file type error")
	}
}
