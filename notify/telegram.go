package notify

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"time"
)

type Notifier struct {
	token  string
	chat   string
	send   func(url string) error
	client *http.Client
}

func New() *Notifier {
	n := &Notifier{client: &http.Client{Timeout: 5 * time.Second}}
	n.send = n.defaultSend
	n.loadEnv()
	return n
}

func (n *Notifier) loadEnv() {
	n.token = os.Getenv("TELEGRAM_BOT_TOKEN")
	n.chat = os.Getenv("TELEGRAM_CHAT_ID")
}

func (n *Notifier) Send(msg string) error {
	if n.token == "" || n.chat == "" {
		slog.Debug("telegram disabled (no token/chat)")
		return nil
	}
	q := url.Values{}
	q.Set("chat_id", n.chat)
	q.Set("text", msg)
	u := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage?%s", n.token, q.Encode())
	return n.send(u)
}

func (n *Notifier) defaultSend(u string) error {
	resp, err := n.client.Get(u)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("telegram status %d", resp.StatusCode)
	}
	return nil
}
