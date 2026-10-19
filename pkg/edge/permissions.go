// Package edge defines portable GoRun Edge site and manager authorization contracts.
// The package is independent of Cloudflare and the GoRun Full runtime.
package edge

import (
 "errors"
 "strings"
)

type Capability string
const (
 ReadContent Capability = "content.read"
 EditContent Capability = "content.edit"
 PublishContent Capability = "content.publish"
 ManageTheme Capability = "theme.manage"
 ManageManagers Capability = "managers.manage"
)

type Grant struct {
 ManagerID string
 Capabilities []Capability
}

type Site struct {
 ID string
 PrimaryManagerID string
 Grants []Grant
}

// Allowed is fail-closed: the owner can manage grants; other managers must
// have an explicit capability. No wildcard or implicit write permission.
func (s Site) Allowed(managerID string, capability Capability) bool {
 if strings.TrimSpace(managerID)=="" || !ValidCapability(capability) { return false }
 if managerID==s.PrimaryManagerID && s.PrimaryManagerID!="" { return true }
 for _, grant := range s.Grants {
  if grant.ManagerID!=managerID { continue }
  for _, c := range grant.Capabilities { if c==capability { return true } }
 }
 return false
}

func ValidCapability(c Capability) bool {
 switch c {
 case ReadContent, EditContent, PublishContent, ManageTheme, ManageManagers: return true
 default: return false
 }
}

var ErrUnauthorized = errors.New("edge: manager not authorized")

// Authorize checks a site-scoped operation before invoking MCP tools.
func Authorize(s Site, managerID string, capability Capability) error {
 if !s.Allowed(managerID,capability) { return ErrUnauthorized }
 return nil
}
