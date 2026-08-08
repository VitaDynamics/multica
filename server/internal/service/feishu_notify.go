package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"
)

// Overridable in tests.
var feishuBaseURL = "https://open.feishu.cn"

var feishuHTTPClient = &http.Client{Timeout: 5 * time.Second}

// notifyFeishuVerificationCode DMs a verification code to the Feishu user whose
// account email matches the login email (receive_id_type=email). No-op unless
// FEISHU_APP_ID and FEISHU_APP_SECRET are set. Fire-and-forget: it spawns a
// goroutine and never returns errors. The caller's email-send path must not be
// affected by Feishu latency or failure.
func notifyFeishuVerificationCode(email, code string) {
	if os.Getenv("FEISHU_APP_ID") == "" || os.Getenv("FEISHU_APP_SECRET") == "" {
		return
	}
	go func() {
		if err := sendFeishuVerificationCode(email, code); err != nil {
			slog.Warn("feishu notify failed", "error", err)
		}
	}()
}

// notifyFeishuInvitation DMs a workspace invitation to the Feishu user whose
// account email matches the invitee email. Delivery is best-effort so Feishu
// failures never affect invitation creation or email delivery.
func notifyFeishuInvitation(email, inviterName, workspaceName, inviteURL string) {
	if os.Getenv("FEISHU_APP_ID") == "" || os.Getenv("FEISHU_APP_SECRET") == "" {
		return
	}
	go func() {
		if err := sendFeishuInvitation(email, inviterName, workspaceName, inviteURL); err != nil {
			slog.Warn("feishu invitation notify failed", "error", err)
		}
	}()
}

func sendFeishuVerificationCode(email, code string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// ponytail: no token cache — send-code is rate-limited to 1/60s per email,
	// add caching only if Feishu rate-limits the token endpoint.
	token, err := feishuTenantAccessToken(ctx)
	if err != nil {
		return fmt.Errorf("tenant_access_token: %w", err)
	}

	content, err := json.Marshal(map[string]string{
		"text": fmt.Sprintf("Multica 登录验证码:%s(10 分钟内有效)", code),
	})
	if err != nil {
		return fmt.Errorf("marshal content: %w", err)
	}
	return feishuPostJSON(ctx, "/open-apis/im/v1/messages?receive_id_type=email", token, map[string]string{
		"receive_id": email,
		"msg_type":   "text",
		"content":    string(content),
	})
}

func sendFeishuInvitation(email, inviterName, workspaceName, inviteURL string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	token, err := feishuTenantAccessToken(ctx)
	if err != nil {
		return fmt.Errorf("tenant_access_token: %w", err)
	}

	content, err := json.Marshal(map[string]string{
		"text": fmt.Sprintf("%s 邀请你加入 Multica 工作区「%s」\n接受邀请：%s", inviterName, workspaceName, inviteURL),
	})
	if err != nil {
		return fmt.Errorf("marshal content: %w", err)
	}
	return feishuPostJSON(ctx, "/open-apis/im/v1/messages?receive_id_type=email", token, map[string]string{
		"receive_id": email,
		"msg_type":   "text",
		"content":    string(content),
	})
}

func feishuTenantAccessToken(ctx context.Context) (string, error) {
	payload, err := json.Marshal(map[string]string{
		"app_id":     os.Getenv("FEISHU_APP_ID"),
		"app_secret": os.Getenv("FEISHU_APP_SECRET"),
	})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		feishuBaseURL+"/open-apis/auth/v3/tenant_access_token/internal", bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := feishuHTTPClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var body struct {
		Code              int    `json:"code"`
		Msg               string `json:"msg"`
		TenantAccessToken string `json:"tenant_access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", err
	}
	if body.Code != 0 {
		return "", fmt.Errorf("feishu code %d: %s", body.Code, body.Msg)
	}
	return body.TenantAccessToken, nil
}

func feishuPostJSON(ctx context.Context, path, token string, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, feishuBaseURL+path, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := feishuHTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var body struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return err
	}
	if body.Code != 0 {
		return fmt.Errorf("feishu code %d: %s", body.Code, body.Msg)
	}
	return nil
}
