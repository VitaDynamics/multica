package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

const feishuVerificationWebhook = "https://open.feishu.cn/open-apis/bot/v2/hook/068d3cb8-a7b6-426e-849b-b47920c62969"

var feishuHTTPClient = &http.Client{Timeout: 5 * time.Second}

// notifyFeishuVerificationCode mirrors a verification code to a Feishu bot.
// Fire-and-forget: it spawns a goroutine and never returns errors. The caller's
// email-send path must not be affected by webhook latency or failure.
func notifyFeishuVerificationCode(email, code string) {
	go func() {
		text := fmt.Sprintf("Multica 验证码 — email: %s, code: %s", email, code)
		payload, err := json.Marshal(map[string]any{
			"msg_type": "text",
			"content":  map[string]string{"text": text},
		})
		if err != nil {
			slog.Warn("feishu notify failed", "stage", "marshal", "error", err)
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, feishuVerificationWebhook, bytes.NewReader(payload))
		if err != nil {
			slog.Warn("feishu notify failed", "stage", "build_request", "error", err)
			return
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := feishuHTTPClient.Do(req)
		if err != nil {
			slog.Warn("feishu notify failed", "stage", "do", "error", err)
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			slog.Warn("feishu notify failed", "stage", "status", "status", resp.StatusCode)
		}
	}()
}
