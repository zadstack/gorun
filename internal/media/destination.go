package media

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"strings"

	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/mailer"
	"github.com/zadstack/gorun/internal/settings"
)

const DestinationPrefix = "storage.destination."

var destinationIDPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)

type Destination struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Purpose   string `json:"purpose"`
	Secret    string `json:"secret"`
	UpdatedAt string `json:"updated_at"`
}

// PrepareDestination binds the location to its ID; credential rotation retains it.
func PrepareDestination(name, purpose string, c S3Config, key []byte) (Destination, error) {
	if purpose != "media" && purpose != "backup" {
		return Destination{}, errors.New("storage: invalid purpose")
	}
	c.PublicURL = ""
	c.Endpoint = strings.TrimRight(c.Endpoint, "/")
	if c.Region == "" {
		c.Region = "auto"
	}
	if _, err := NewS3Storage(c, nil, false); err != nil {
		return Destination{}, err
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return Destination{}, err
	}
	encrypted, err := mailer.EncryptSecret(key, string(raw))
	if err != nil {
		return Destination{}, err
	}
	return Destination{ID: destinationIdentity(purpose, c), Name: name, Purpose: purpose, Secret: encrypted, UpdatedAt: db.Now()}, nil
}

func SaveDestinationTx(ctx context.Context, tx *sql.Tx, store *settings.Store, d Destination) error {
	if !destinationIDPattern.MatchString(d.ID) || (d.Purpose != "media" && d.Purpose != "backup") || d.Secret == "" {
		return errors.New("storage: invalid destination")
	}
	if err := store.SetTx(ctx, tx, DestinationPrefix+d.ID, d); err != nil {
		return err
	}
	return store.SetTx(ctx, tx, "storage.active."+d.Purpose, d.ID)
}
func SaveDestination(ctx context.Context, database *sql.DB, store *settings.Store, d Destination) error {
	return db.InTx(ctx, database, func(tx *sql.Tx) error { return SaveDestinationTx(ctx, tx, store, d) })
}
func LoadDestination(ctx context.Context, store *settings.Store, id string) (Destination, error) {
	var d Destination
	if !destinationIDPattern.MatchString(id) {
		return d, errors.New("storage: invalid destination ID")
	}
	if err := store.Get(ctx, DestinationPrefix+id, &d); err != nil {
		return d, err
	}
	if d.ID != id || (d.Purpose != "media" && d.Purpose != "backup") {
		return d, errors.New("storage: invalid destination")
	}
	return d, nil
}
func DestinationConfig(d Destination, key []byte) (S3Config, error) {
	var c S3Config
	raw, err := mailer.DecryptSecret(key, d.Secret)
	if err != nil {
		return c, errors.New("storage: destination credentials unavailable")
	}
	if err = json.Unmarshal([]byte(raw), &c); err != nil {
		return c, errors.New("storage: invalid destination credentials")
	}
	if _, err := NewS3Storage(c, nil, false); err != nil || destinationIdentity(d.Purpose, c) != d.ID {
		return c, errors.New("storage: destination identity mismatch")
	}
	return c, nil
}

func destinationIdentity(purpose string, c S3Config) string {
	sum := sha256.Sum256([]byte(purpose + "\n" + c.Endpoint + "\n" + c.Region + "\n" + c.Bucket))
	return hex.EncodeToString(sum[:16])
}
