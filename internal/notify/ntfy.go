package notify

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strconv"
	"strings"
	"time"
)

// Config holds the effective (post-default) ntfy settings for the Sender.
type Config struct {
	BaseURL            string
	Topic              string
	Token              string
	Priority           int
	TimeoutSeconds     int
	AttachLogOnFailure bool
	MaxLogBytes        int
}

type Attachment struct {
	Filename    string
	ContentType string
	Data        []byte
}

type Notification struct {
	Title      string
	Message    string
	Tags       []string
	Priority   int
	Attachment *Attachment
}

type Sender struct {
	cfg    Config
	client *http.Client
}

func NewSender(cfg Config) *Sender {
	timeout := time.Duration(cfg.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	return &Sender{
		cfg:    cfg,
		client: &http.Client{Timeout: timeout},
	}
}

// Configured reports whether the sender has enough settings to publish.
func (s *Sender) Configured() bool {
	return s != nil && s.cfg.BaseURL != "" && s.cfg.Topic != ""
}

func (s *Sender) DefaultPriority() int {
	return s.cfg.Priority
}

func (s *Sender) Send(ctx context.Context, n Notification) error {
	if !s.Configured() {
		return fmt.Errorf("ntfy not configured")
	}

	url := strings.TrimRight(s.cfg.BaseURL, "/") + "/" + s.cfg.Topic

	var body io.Reader
	contentType := "text/plain; charset=utf-8"

	if n.Attachment != nil && len(n.Attachment.Data) > 0 {
		var buf bytes.Buffer
		writer := multipart.NewWriter(&buf)
		writeField(writer, "title", n.Title)
		writeField(writer, "message", n.Message)
		if len(n.Tags) > 0 {
			writeField(writer, "tags", strings.Join(n.Tags, ","))
		}
		if n.Priority > 0 {
			writeField(writer, "priority", strconv.Itoa(n.Priority))
		}

		partContentType := n.Attachment.ContentType
		if partContentType == "" {
			partContentType = "application/octet-stream"
		}
		header := make(textproto.MIMEHeader)
		header.Set("Content-Disposition",
			fmt.Sprintf(`form-data; name="file"; filename=%q`, escapeQuotes(n.Attachment.Filename)))
		header.Set("Content-Type", partContentType)
		part, err := writer.CreatePart(header)
		if err != nil {
			return fmt.Errorf("create multipart part: %w", err)
		}
		if _, err := part.Write(n.Attachment.Data); err != nil {
			return fmt.Errorf("write attachment: %w", err)
		}
		if err := writer.Close(); err != nil {
			return fmt.Errorf("close multipart writer: %w", err)
		}
		contentType = writer.FormDataContentType()
		body = &buf
	} else {
		body = strings.NewReader(n.Message)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, body)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", contentType)
	if s.cfg.Token != "" {
		req.Header.Set("Authorization", "Bearer "+s.cfg.Token)
	}

	if n.Attachment == nil {
		req.Header.Set("Title", n.Title)
		if len(n.Tags) > 0 {
			req.Header.Set("Tags", strings.Join(n.Tags, ","))
		}
		if n.Priority > 0 {
			req.Header.Set("Priority", strconv.Itoa(n.Priority))
		}
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("send ntfy: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("ntfy returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	return nil
}

func writeField(writer *multipart.Writer, key, value string) {
	if value == "" {
		return
	}
	_ = writer.WriteField(key, value)
}

func escapeQuotes(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	return s
}
