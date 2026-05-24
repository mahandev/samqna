package notify

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSend_NoConfigIsNoOp(t *testing.T) {
	t.Setenv("TELEGRAM_BOT_TOKEN", "")
	t.Setenv("TELEGRAM_CHAT_ID", "")
	n := New()
	require.NoError(t, n.Send("hello"))
}

func TestSend_BuildsCorrectURL(t *testing.T) {
	t.Setenv("TELEGRAM_BOT_TOKEN", "TOK")
	t.Setenv("TELEGRAM_CHAT_ID", "CHAT")
	var captured string
	n := &Notifier{
		send: func(url string) error { captured = url; return nil },
	}
	n.loadEnv()
	require.NoError(t, n.Send("hi there"))
	require.Contains(t, captured, "https://api.telegram.org/botTOK/sendMessage")
	require.Contains(t, captured, "chat_id=CHAT")
	require.Contains(t, captured, "text=hi+there")
}
