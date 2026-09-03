package main

import "testing"

func TestValidateConfig(t *testing.T) {
	if err := validateConfig([]byte(`{"base_url":"https://example.invalid/v1","api_key":"secret","model_name":"demo"}`)); err != nil {
		t.Fatal(err)
	}
	if err := validateConfig([]byte(`{"base_url":"","api_key":"","model_name":""}`)); err == nil {
		t.Fatal("expected missing fields to be rejected")
	}
}
