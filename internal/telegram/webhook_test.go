package telegram

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func TestWebhookHandler(t *testing.T) {
	bot := &Bot{
		api:    &tgbotapi.BotAPI{},
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	handler := bot.webhookHandler(context.Background(), "test_secret", "/hook")

	tests := []struct {
		name       string
		method     string
		path       string
		secret     string
		body       string
		wantStatus int
	}{
		{name: "rejects wrong method", method: http.MethodGet, path: "/hook", secret: "test_secret", wantStatus: http.StatusMethodNotAllowed},
		{name: "rejects wrong path", method: http.MethodPost, path: "/other", secret: "test_secret", wantStatus: http.StatusNotFound},
		{name: "rejects wrong secret", method: http.MethodPost, path: "/hook", secret: "wrong", wantStatus: http.StatusUnauthorized},
		{name: "rejects malformed update", method: http.MethodPost, path: "/hook", secret: "test_secret", body: "{", wantStatus: http.StatusBadRequest},
		{name: "accepts update", method: http.MethodPost, path: "/hook", secret: "test_secret", body: `{"update_id":1}`, wantStatus: http.StatusOK},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
			request.Header.Set("X-Telegram-Bot-Api-Secret-Token", test.secret)
			response := httptest.NewRecorder()

			handler.ServeHTTP(response, request)

			if response.Code != test.wantStatus {
				t.Errorf("status = %d, want %d", response.Code, test.wantStatus)
			}
		})
	}
}
