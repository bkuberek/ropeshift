package notify

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// The headers a delivery carries. The delivery repeats on a retry, so a receiver can drop a second
// copy, and the signature is the body's HMAC-SHA256 under the key, so the key never crosses the wire.
const (
	HeaderDelivery  = "Ropeshift-Delivery"
	HeaderSignature = "Ropeshift-Signature"
)

// webhookTimeout bounds one delivery, because a far end that hangs must not hold the tick.
const webhookTimeout = 10 * time.Second

// Dial opens one connection. Every connection this package opens goes through it, and a test that
// holds this is how anything can say that no connection opened.
type Dial func(ctx context.Context, network, address string) (net.Conn, error)

// WebhookConfig is one workspace's webhook. URL comes from a published policy, which refused a secret
// in it, and Key from the daemon's environment.
type WebhookConfig struct {
	URL  string
	Key  string
	Dial Dial
}

// Webhook posts a notice as JSON.
type Webhook struct {
	url    string
	key    []byte
	client *http.Client
}

// NewWebhook returns a channel. It opens nothing until a notice goes out. It keeps its own transport,
// so no proxy a variable names sees a delivery, and it follows no redirect, because the only address
// a notice may go to is the one a member published.
func NewWebhook(config WebhookConfig) *Webhook {
	dial := config.Dial
	if dial == nil {
		dial = (&net.Dialer{Timeout: webhookTimeout}).DialContext
	}
	return &Webhook{url: config.URL, key: []byte(config.Key), client: &http.Client{
		Timeout:       webhookTimeout,
		Transport:     &http.Transport{DialContext: dial, DisableKeepAlives: true, ResponseHeaderTimeout: webhookTimeout},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

// Channel names this channel.
func (*Webhook) Channel() string { return ChannelWebhook }

func (w *Webhook) signature(body []byte) string {
	mac := hmac.New(sha256.New, w.key)
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// failure says what went wrong and nothing of where. Go's errors from a request name the URL and the
// address it dialed, and this text reaches a log and the ledger.
func failure(ctx context.Context, err error) error {
	var network net.Error
	switch {
	case ctx.Err() != nil:
		return ctx.Err()
	case errors.As(err, &network) && network.Timeout():
		return errors.New("the webhook did not answer in time")
	}
	return errors.New("the webhook could not be reached")
}

// Notify posts one notice. Anything but a 2xx is a refusal, a redirect among them.
func (w *Webhook) Notify(ctx context.Context, notice ledger.Notice) error {
	body, err := json.Marshal(notice)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, w.url, bytes.NewReader(body))
	if err != nil {
		return errors.New("the webhook's address does not parse")
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "ropeshift")
	request.Header.Set(HeaderDelivery, notice.Delivery)
	if len(w.key) != 0 {
		request.Header.Set(HeaderSignature, w.signature(body))
	}
	response, err := w.client.Do(request)
	if err != nil {
		return failure(ctx, err)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<16))
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("the webhook answered %d", response.StatusCode)
	}
	return nil
}

var _ ports.Notifier = (*Webhook)(nil)
