package content

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
)

// A missing guard preserves the legacy contract; explicit null must never
// silently downgrade a caller that supplied the guard field.
func (in *linkInput) UnmarshalJSON(raw []byte) error {
	type plain linkInput
	var value plain
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	for key, expected := range fields {
		if strings.EqualFold(key, "expected_ids") {
			value.hasExpectedIDs = true
			if bytes.Equal(bytes.TrimSpace(expected), []byte("null")) {
				return errors.New("expected_ids must be an array when provided")
			}
		}
	}
	*in = linkInput(value)
	return nil
}
