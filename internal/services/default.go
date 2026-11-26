package services

import "github.com/zadstack/gorun/internal/kernel"

// Default is the site's five services over d, in the order every surface
// lists them: mail, storage, chat, analytics, backups.
func Default(d *kernel.Deps) *Registry {
	return NewRegistry(d, Mail(d), Storage(d), Chat(d), Analytics(d), Backups(d))
}
