package mailbox

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/mail"
	"strings"
	"time"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
)

// IMAPConfig configures a receive-only IMAP/TLS mailbox.
type IMAPConfig struct {
	Address  string
	Username string
	Password string
}

// IMAPSource reads a bounded recent window over standard IMAP/TLS.
type IMAPSource struct {
	config IMAPConfig
}

// NewIMAPSource validates the receive endpoint without making a connection.
func NewIMAPSource(config IMAPConfig) (*IMAPSource, error) {
	config.Address = strings.TrimSpace(config.Address)
	config.Username = strings.TrimSpace(config.Username)
	if config.Address == "" || config.Username == "" || config.Password == "" {
		return nil, errors.New("mailbox: IMAP address, username, and password are required")
	}
	host, port, err := net.SplitHostPort(config.Address)
	if err != nil || strings.TrimSpace(host) == "" || port == "" {
		return nil, errors.New("mailbox: IMAP address must be host:port")
	}
	return &IMAPSource{config: config}, nil
}

// FetchRecent retrieves at most limit newest messages without changing flags.
func (source *IMAPSource) FetchRecent(
	ctx context.Context,
	limit int,
) ([]Message, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	host, _, _ := net.SplitHostPort(source.config.Address)
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	connection, err := dialer.DialContext(ctx, "tcp", source.config.Address)
	if err != nil {
		return nil, fmt.Errorf("mailbox: connect IMAP: %w", err)
	}
	tlsConnection := tls.Client(connection, &tls.Config{
		ServerName: host, MinVersion: tls.VersionTLS12,
	})
	if err := tlsConnection.HandshakeContext(ctx); err != nil {
		_ = connection.Close()
		return nil, fmt.Errorf("mailbox: secure IMAP: %w", err)
	}
	imapClient, err := client.New(tlsConnection)
	if err != nil {
		_ = tlsConnection.Close()
		return nil, err
	}
	imapClient.ErrorLog = log.New(io.Discard, "", 0)
	imapClient.Timeout = 20 * time.Second
	defer func() {
		_ = imapClient.Logout()
	}()
	if err := imapClient.Login(source.config.Username, source.config.Password); err != nil {
		return nil, fmt.Errorf("mailbox: authenticate IMAP: %w", err)
	}
	status, err := imapClient.Select("INBOX", true)
	if err != nil {
		return nil, fmt.Errorf("mailbox: select inbox: %w", err)
	}
	if status.Messages == 0 {
		return []Message{}, nil
	}
	start := uint32(1)
	if status.Messages > uint32(limit) {
		start = status.Messages - uint32(limit) + 1
	}
	set := new(imap.SeqSet)
	set.AddRange(start, status.Messages)
	section := &imap.BodySectionName{Peek: true}
	channel := make(chan *imap.Message, limit)
	fetchErrors := make(chan error, 1)
	go func() {
		fetchErrors <- imapClient.Fetch(
			set,
			[]imap.FetchItem{
				imap.FetchUid, imap.FetchEnvelope, imap.FetchInternalDate,
				section.FetchItem(),
			},
			channel,
		)
	}()
	result := make([]Message, 0, limit)
	for found := range channel {
		if found == nil {
			continue
		}
		body := found.GetBody(section)
		if body == nil {
			continue
		}
		raw, err := io.ReadAll(io.LimitReader(body, maxRawMessage+1))
		if err != nil || len(raw) > maxRawMessage {
			continue
		}
		message := Message{
			UID: found.Uid, ReceivedAt: found.InternalDate, Raw: raw,
		}
		if found.Envelope != nil {
			message.Subject = found.Envelope.Subject
			if len(found.Envelope.From) > 0 {
				from := found.Envelope.From[0]
				message.From = (&mail.Address{
					Name:    from.PersonalName,
					Address: from.MailboxName + "@" + from.HostName,
				}).String()
			}
		}
		result = append(result, message)
	}
	if err := <-fetchErrors; err != nil {
		return nil, fmt.Errorf("mailbox: fetch inbox: %w", err)
	}
	return result, nil
}
