package service

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSendFeishuVerificationCode(t *testing.T) {
	var gotMessage map[string]string
	var gotAuth, gotQuery string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/open-apis/auth/v3/tenant_access_token/internal":
			var creds map[string]string
			if err := json.NewDecoder(r.Body).Decode(&creds); err != nil || creds["app_id"] != "cli_test" {
				t.Errorf("bad token request: %v %v", err, creds)
			}
			json.NewEncoder(w).Encode(map[string]any{"code": 0, "tenant_access_token": "t-abc"})
		case "/open-apis/im/v1/messages":
			gotAuth = r.Header.Get("Authorization")
			gotQuery = r.URL.RawQuery
			if err := json.NewDecoder(r.Body).Decode(&gotMessage); err != nil {
				t.Errorf("decode message body: %v", err)
			}
			json.NewEncoder(w).Encode(map[string]any{"code": 0})
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	oldBase := feishuBaseURL
	feishuBaseURL = srv.URL
	defer func() { feishuBaseURL = oldBase }()
	t.Setenv("FEISHU_APP_ID", "cli_test")
	t.Setenv("FEISHU_APP_SECRET", "secret")

	if err := sendFeishuVerificationCode("user@example.com", "123456"); err != nil {
		t.Fatalf("sendFeishuVerificationCode: %v", err)
	}

	if gotAuth != "Bearer t-abc" {
		t.Errorf("Authorization = %q, want Bearer t-abc", gotAuth)
	}
	if gotQuery != "receive_id_type=email" {
		t.Errorf("query = %q, want receive_id_type=email", gotQuery)
	}
	if gotMessage["receive_id"] != "user@example.com" || gotMessage["msg_type"] != "text" {
		t.Errorf("message = %v", gotMessage)
	}
	if !strings.Contains(gotMessage["content"], "123456") {
		t.Errorf("content missing code: %q", gotMessage["content"])
	}
	var inner map[string]string
	if err := json.Unmarshal([]byte(gotMessage["content"]), &inner); err != nil {
		t.Errorf("content is not a JSON string: %v", err)
	}
	if strings.Contains(gotMessage["content"], "user@example.com") {
		t.Errorf("content leaks email: %q", gotMessage["content"])
	}
}

func TestNotifyFeishuVerificationCodeNoopWithoutEnv(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request to %s", r.URL.Path)
	}))
	defer srv.Close()

	oldBase := feishuBaseURL
	feishuBaseURL = srv.URL
	defer func() { feishuBaseURL = oldBase }()
	t.Setenv("FEISHU_APP_ID", "")
	t.Setenv("FEISHU_APP_SECRET", "")

	notifyFeishuVerificationCode("user@example.com", "123456")
}
