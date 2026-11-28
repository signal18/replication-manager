// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

// Package telegram sends a message through the Telegram Bot API (sendMessage) with net/http
// and JSON only. The bot token is part of the request URL
// (https://api.telegram.org/bot<token>/sendMessage): every error this package returns has
// the token replaced, since net/http quotes the URL in its errors (F10).
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DefaultAPIURL is the Bot API's base URL (alert-telegram-api-url).
const DefaultAPIURL = "https://api.telegram.org"

// ParseModeMarkdownV2 is the one formatted mode supported; any other value sends plain text.
const ParseModeMarkdownV2 = "MarkdownV2"

const tokenMask = "<alert-telegram-bot-token>"

// Client sends to one or more chats with one bot.
type Client struct {
	APIURL    string
	Token     string
	ChatIDs   []string
	ParseMode string
	HTTP      *http.Client
}

// New builds a client. chatIDs is a comma-separated list (a numeric id, -100... for a
// group or channel, or @channelname).
func New(apiURL, token, chatIDs, parseMode string, timeout time.Duration) *Client {
	if strings.TrimSpace(apiURL) == "" {
		apiURL = DefaultAPIURL
	}
	c := &Client{APIURL: strings.TrimRight(strings.TrimSpace(apiURL), "/"), Token: strings.TrimSpace(token), HTTP: &http.Client{Timeout: timeout}}
	if parseMode == ParseModeMarkdownV2 {
		c.ParseMode = ParseModeMarkdownV2
	}
	for _, id := range strings.Split(chatIDs, ",") {
		if id = strings.TrimSpace(id); id != "" {
			c.ChatIDs = append(c.ChatIDs, id)
		}
	}
	return c
}

// markdownV2Special are the characters MarkdownV2 requires escaped outside an entity
// (https://core.telegram.org/bots/api#markdownv2-style); the backslash itself first.
const markdownV2Special = "\\_*[]()~`>#+-=|{}.!"

// EscapeMarkdownV2 escapes s for a MarkdownV2 message: every special character gets a
// backslash, so the text shows as it is.
func EscapeMarkdownV2(s string) string {
	var b strings.Builder
	b.Grow(len(s) + len(s)/4)
	for _, r := range s {
		if strings.ContainsRune(markdownV2Special, r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// Format is the message for a title and a body: plain, or the title in bold and both
// escaped in MarkdownV2.
func (c *Client) Format(title, body string) string {
	if c.ParseMode == ParseModeMarkdownV2 {
		return "*" + EscapeMarkdownV2(title) + "*\n" + EscapeMarkdownV2(body)
	}
	return title + "\n" + body
}

type sendMessage struct {
	ChatID    string `json:"chat_id"`
	Text      string `json:"text"`
	ParseMode string `json:"parse_mode,omitempty"`
}

type apiResponse struct {
	OK          bool   `json:"ok"`
	ErrorCode   int    `json:"error_code"`
	Description string `json:"description"`
}

// Send posts text to every chat. It fails when a chat did not get it; the error never
// holds the token.
func (c *Client) Send(ctx context.Context, text string) error {
	if c.Token == "" {
		return errors.New("alert-telegram-bot-token is empty")
	}
	if len(c.ChatIDs) == 0 {
		return errors.New("alert-telegram-chat-id is empty")
	}
	var errs []error
	for _, id := range c.ChatIDs {
		if err := c.send(ctx, id, text); err != nil {
			errs = append(errs, fmt.Errorf("chat %s: %w", id, err))
		}
	}
	return c.redact(errors.Join(errs...))
}

func (c *Client) send(ctx context.Context, chatID, text string) error {
	body, err := json.Marshal(sendMessage{ChatID: chatID, Text: text, ParseMode: c.ParseMode})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.APIURL+"/bot"+c.Token+"/sendMessage", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	var r apiResponse
	if jerr := json.Unmarshal(raw, &r); jerr != nil || resp.StatusCode/100 != 2 || !r.OK {
		desc := r.Description
		if desc == "" {
			desc = strings.TrimSpace(string(raw))
			if len(desc) > 256 {
				desc = desc[:256]
			}
		}
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, desc)
	}
	return nil
}

// redact replaces the token in an error's text: net/http quotes the request URL, which
// holds it.
func (c *Client) redact(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	if c.Token != "" {
		msg = strings.ReplaceAll(msg, c.Token, tokenMask)
	}
	return errors.New(msg)
}
