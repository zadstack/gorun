package site

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/zadstack/gorun/internal/mailer"
)

// Form settings. The contact.* flags are public (themes read them with
// {{setting}} to show the file input); forms.* stay private because the
// recipient list holds staff addresses.
const (
	SettingFormsNotify           = "forms.notify"              // bool, default true: mail each stored submission
	SettingFormsRecipients       = "forms.recipients"          // "a@x.com, b@y.com"; empty: contact.email, else the first admin
	SettingAttachments           = "contact.attachments"       // bool, default false: accept one file per submission
	SettingAttachmentMaxMB       = "contact.attachment_max_mb" // 1–20, default 5
	defaultAttachmentMaxMB       = 5
	maxAttachmentMaxMB           = 20
	mailAttachmentLimit    int64 = 8 << 20 // larger files are linked from the email, not attached
)

// FormSettingKeys lists the keys ValidateFormSetting accepts.
var FormSettingKeys = []string{SettingFormsNotify, SettingFormsRecipients, SettingAttachments, SettingAttachmentMaxMB}

// ErrFormSetting is wrapped by every ValidateFormSetting rejection.
var ErrFormSetting = errors.New("invalid form setting")

// formConfig is the effective form configuration.
type formConfig struct {
	Notify      bool
	Recipients  []string
	Attachments bool
	MaxMB       int
}

func (c formConfig) maxBytes() int64 { return int64(c.MaxMB) << 20 }

// RecipientsText is the recipient list as stored (for the settings form).
func (c formConfig) RecipientsText() string { return strings.Join(c.Recipients, ", ") }

func (h *handlers) formConfig(ctx context.Context) formConfig {
	c := formConfig{Notify: h.boolSetting(ctx, SettingFormsNotify, true), MaxMB: defaultAttachmentMaxMB,
		Attachments: h.boolSetting(ctx, SettingAttachments, false)}
	if list, err := parseRecipients(h.d.Settings.GetString(ctx, SettingFormsRecipients, "")); err == nil {
		c.Recipients = list
	} else {
		h.d.Log.Warn("site: invalid forms.recipients setting; using the default recipient", "err", err)
	}
	var n json.Number
	if err := h.d.Settings.Get(ctx, SettingAttachmentMaxMB, &n); err == nil {
		if v, err := strconv.Atoi(n.String()); err == nil && v >= 1 && v <= maxAttachmentMaxMB {
			c.MaxMB = v
		}
	}
	return c
}

func (h *handlers) boolSetting(ctx context.Context, key string, def bool) bool {
	var v any
	if err := h.d.Settings.Get(ctx, key, &v); err != nil || v == nil {
		return def
	}
	switch b := v.(type) {
	case bool:
		return b
	case string:
		if p, err := strconv.ParseBool(b); err == nil {
			return p
		}
	}
	return def
}

// parseRecipients splits a comma, semicolon or newline separated list of
// plain addresses (at most 10, duplicates dropped).
func parseRecipients(s string) ([]string, error) { return mailer.ParseRecipients(s) }

// ValidateFormSetting validates one form setting given as text (the admin
// form, `gorun settings set`) and returns the value to store: a bool for
// the flags, an int for the size and a normalised list for recipients.
func ValidateFormSetting(key, text string) (any, error) {
	fail := func(msg string) (any, error) { return nil, errors.Join(ErrFormSetting, errors.New(msg)) }
	text = strings.TrimSpace(text)
	switch key {
	case SettingFormsNotify, SettingAttachments:
		b, err := strconv.ParseBool(text)
		if err != nil {
			return fail("true or false")
		}
		return b, nil
	case SettingAttachmentMaxMB:
		n, err := strconv.Atoi(text)
		if err != nil || n < 1 || n > maxAttachmentMaxMB {
			return fail("a whole number of megabytes from 1 to 20")
		}
		return n, nil
	case SettingFormsRecipients:
		list, err := parseRecipients(text)
		if err != nil {
			return fail(err.Error())
		}
		return strings.Join(list, ", "), nil
	}
	return fail("unknown key " + key)
}
