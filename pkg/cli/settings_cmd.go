package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"

	"github.com/zadstack/gorun/internal/admin"
	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/seo"
	"github.com/zadstack/gorun/internal/settings/settingsmod"
	"github.com/zadstack/gorun/internal/site"
)

const settingsUsage = "usage: gorun settings get <key> | set <key> <json-or-string> | unset <key> [--data dir]"

var (
	settingKeyRe = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,127}$`)
	themeSlugRe  = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
	// secretKeyRe matches settings stored encrypted by the admin (mail
	// password, Stripe keys…): never printed, never written from the shell.
	secretKeyRe = regexp.MustCompile(`password|secret|api_key|private`)
)

// settingsCmd implements `gorun settings get|set|unset`, the recovery
// path when a setting (e.g. site.base_url) makes the admin unusable. It
// boots only the settings module (no workers, no themes) on the data dir.
// Writes are validated like the admin screens where a validator exists
// and audited as the system actor.
func settingsCmd(args []string, out io.Writer) error {
	if len(args) < 2 {
		return errors.New(settingsUsage)
	}
	sub, key := args[0], args[1]
	rest := args[2:]
	var raw string
	switch sub {
	case "set":
		if len(rest) < 1 {
			return errors.New(settingsUsage)
		}
		raw, rest = rest[0], rest[1:]
	case "get", "unset":
	default:
		return errors.New(settingsUsage)
	}
	if !settingKeyRe.MatchString(key) {
		return fmt.Errorf("settings: invalid key %q", key)
	}
	var value any
	if sub == "set" {
		v, err := cliSettingValue(key, raw)
		if err != nil {
			return err
		}
		value = v
	}
	cfg, err := config.Load(rest)
	if err != nil {
		return err
	}
	ctx := context.Background()
	app, err := kernel.BootWithOptions(ctx, cfg, []kernel.Module{settingsmod.New()}, kernel.BootOptions{StartJobs: false})
	if err != nil {
		return err
	}
	defer app.Close()
	st := app.Deps.Settings
	switch sub {
	case "get":
		var v json.RawMessage
		if err := st.Get(ctx, key, &v); err != nil {
			return &exitError{code: 1, msg: "settings: " + key + " is not set"}
		}
		if secretKeyRe.MatchString(key) {
			fmt.Fprintln(out, `"(set, hidden)"`)
			return nil
		}
		fmt.Fprintln(out, string(v))
	case "set":
		if err := st.Set(ctx, key, value); err != nil {
			return err
		}
		app.Deps.Audit.Record(ctx, "settings.update", "settings", "", map[string]any{"keys": []string{key}, "via": "cli"})
		fmt.Fprintf(out, "set %s\n", key)
	case "unset":
		if err := st.Delete(ctx, key); err != nil {
			return err
		}
		app.Deps.Audit.Record(ctx, "settings.update", "settings", "", map[string]any{"keys": []string{key}, "via": "cli", "unset": true})
		fmt.Fprintf(out, "unset %s\n", key)
	}
	return nil
}

// cliSettingValue turns the shell argument into the stored value: JSON when
// it parses, else the plain string. Keys with an admin validator get the
// same validation and normalisation as the admin screens.
func cliSettingValue(key, raw string) (any, error) {
	if secretKeyRe.MatchString(key) {
		return nil, fmt.Errorf("settings: %s is a secret stored encrypted; set it in the admin", key)
	}
	if strings.HasPrefix(key, "ai.") {
		return nil, fmt.Errorf("settings: use `gorun ai set` (validated like Settings → AI) for %s", key)
	}
	var parsed any
	isJSON := json.Unmarshal([]byte(raw), &parsed) == nil
	text := raw // validated keys take text: a JSON string is unquoted, anything else is used verbatim
	if s, ok := parsed.(string); isJSON && ok {
		text = s
	}
	invalid := func(err error) error { return fmt.Errorf("settings: invalid value for %s: %v", key, err) }
	if slices.Contains(site.FormSettingKeys, key) {
		v, err := site.ValidateFormSetting(key, text)
		if err != nil {
			return nil, invalid(err)
		}
		return v, nil
	}
	switch key {
	case "site.title", "site.tagline", "site.locale", "site.timezone", "site.base_url", "contact.email":
		v, err := admin.ValidateGeneralSetting(key, text)
		if err != nil {
			return nil, invalid(err)
		}
		return v, nil
	case site.SettingMaintenance:
		switch strings.ToLower(text) {
		case "true", "1", "on":
			return true, nil
		case "false", "0", "off":
			return false, nil
		}
		return nil, invalid(errors.New("true or false"))
	case "site.posts_per_page":
		n, ok := admin.PostsPerPage(text)
		if !ok {
			return nil, invalid(errors.New("a whole number from 1 to 100"))
		}
		return n, nil
	case seo.SettingTitleTemplate, seo.SettingDefaultDescription, seo.SettingDefaultOGImage, seo.SettingRobotsTxt:
		v, err := seo.ValidateSetting(key, text)
		if err != nil {
			return nil, invalid(err)
		}
		return v, nil
	case "site.theme":
		if !themeSlugRe.MatchString(text) {
			return nil, invalid(errors.New("a theme slug such as default or business"))
		}
		return text, nil
	case "site.front_page", "site.logo":
		if len(text) > 64 || strings.ContainsAny(text, " \t\r\n/") {
			return nil, invalid(errors.New("an id, or empty"))
		}
		return text, nil
	}
	if isJSON {
		return parsed, nil
	}
	return raw, nil
}
