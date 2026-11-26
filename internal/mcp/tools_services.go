package mcp

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/services"
)

// scopeCapabilityTest is the scope of test_service (the admin's capability
// tests use the same permission).
const scopeCapabilityTest = "system.capability_test"

type serviceNameInput struct {
	Name string `json:"name" enum:"mail,storage,chat,analytics,backups" jsonschema:"the service"`
}

type setServiceInput struct {
	Name   string         `json:"name" enum:"mail,storage,chat,analytics,backups" jsonschema:"the service"`
	Values map[string]any `json:"values" jsonschema:"field name → new value (string, number or boolean); only these fields change, the others and the stored secret stay. Only the service's declared fields; never a secret (password, api_key, keys): it is refused"`
}

type listServicesOut struct {
	Services []services.Status `json:"services"`
}

// serviceTools is the services toolset over reg: the same registry and
// rules as gorun service and /api/v1/site/services, audited via mcp.
func (s *server) serviceTools(reg *services.Registry) []kernel.MCPTool {
	s.parkChecks["set_service"] = func(ctx context.Context, args json.RawMessage) error {
		var in setServiceInput
		if err := decode(args, &in); err != nil {
			return err
		}
		_, err := serviceValues(reg, in)
		return err
	}
	s.storedResults["set_service"] = func(data any) any {
		if res, ok := data.(services.SetResult); ok {
			return res.Redacted(storedLinkNote)
		}
		return data
	}
	return []kernel.MCPTool{
		{Name: "list_services", Title: "List services", Toolset: kernel.MCPToolsetServices, ReadOnly: true, Idempotent: true, Scopes: []string{scopeSettings}, Input: struct{}{},
			Description: "The site's services (mail, storage, chat, analytics, backups) and their status: state off, configured or incomplete, what is missing, the fields, and secret_set. Never a secret's value.",
			Handler: bind(func(ctx context.Context, _ struct{}) (any, error) {
				list, err := reg.List(ctx)
				if err != nil {
					return nil, err
				}
				return listServicesOut{Services: list}, nil
			})},
		{Name: "get_service", Title: "Get service", Toolset: kernel.MCPToolsetServices, ReadOnly: true, Idempotent: true, Scopes: []string{scopeSettings}, Input: serviceNameInput{},
			Description: "One service's status (fields, state, missing, secret_set), without its secret.",
			Handler: bind(func(ctx context.Context, in serviceNameInput) (any, error) {
				svc, ok := reg.Get(in.Name)
				if !ok {
					return nil, notFound("service " + in.Name)
				}
				return svc.Status(ctx)
			})},
		{Name: "set_service", Title: "Set service", Toolset: kernel.MCPToolsetServices, Destructive: true, Sensitive: true, Scopes: []string{scopeSettings}, Input: setServiceInput{},
			Description: "Change only the given fields of a service, validated like the admin; nothing is saved if one is invalid. A secret is never an argument: " +
				"when the service still lacks it the answer carries needs_secret, a one-time admin link (15 minutes, once) for a person to type it, and the CLI line; " +
				"values that need the secret to be saved (a new storage bucket) are held with the link and applied with the secret (pending). A key that is not a field of the service is refused. Needs human approval.",
			Handler: bind(func(ctx context.Context, in setServiceInput) (any, error) {
				values, err := serviceValues(reg, in)
				if err != nil {
					return nil, err
				}
				res, fields, err := reg.SetRemote(ctx, in.Name, values, "mcp")
				switch {
				case errors.Is(err, services.ErrUnknownService):
					return nil, notFound("service " + in.Name)
				case err != nil:
					return nil, err
				case len(fields) > 0:
					return nil, invalid("invalid values; nothing was saved", fields)
				}
				return res, nil
			}),
			Preview: func(ctx context.Context, args json.RawMessage) (string, error) {
				if !auth.Can(ctx, scopeSettings) {
					return "", errForbidden()
				}
				var in setServiceInput
				if err := decode(args, &in); err != nil {
					return "", err
				}
				values, bad := services.StringValues(in.Values)
				for k := range bad {
					values[k] = "(not a string, number or boolean)"
				}
				return reg.Describe(ctx, in.Name, values)
			}},
		{Name: "test_service", Title: "Test service", Toolset: kernel.MCPToolsetServices, OpenWorld: true, Scopes: []string{scopeCapabilityTest}, Input: serviceNameInput{},
			Description: "Run the service's safe test: mail sends one test message to the key owner's own address; chat and storage have no server-side test and say so.",
			Handler: bind(func(ctx context.Context, in serviceNameInput) (any, error) {
				res, err := reg.TestFor(ctx, in.Name)
				var te *services.TestError
				switch {
				case errors.Is(err, services.ErrUnknownService):
					return nil, notFound("service " + in.Name)
				case errors.As(services.ClassifyTestError(err), &te):
					return nil, invalid(te.Error(), map[string]string{te.Field: te.Key})
				case err != nil:
					return nil, err
				}
				return res, nil
			})},
	}
}

// storedLinkNote is the needs_secret note of an approved set_service
// result as stored (and returned by check_approval): its link is redacted.
const storedLinkNote = "The one-time link is redacted in a stored result. Call set_service again for a fresh link " +
	"(a key with mcp.approve_self gets it in the answer), or run the CLI line on the site."

// serviceValues checks a set_service call before it is parked or run: a
// known service, at least one value, scalar values only, and only keys the
// service declares as fields (so a secret, under its own name or any other,
// is never stored with a parked call).
func serviceValues(reg *services.Registry, in setServiceInput) (map[string]string, error) {
	svc, ok := reg.Get(in.Name)
	if !ok {
		return nil, notFound("service " + in.Name)
	}
	if len(in.Values) == 0 {
		return nil, invalid("give at least one value", map[string]string{"values": "services.error.required"})
	}
	values, bad := services.StringValues(in.Values)
	keys := make(map[string]string, len(in.Values))
	for k := range in.Values {
		keys[k] = ""
	}
	for k, v := range services.RemoteFields(svc, keys) {
		bad[k] = v // whatever its value's type
	}
	if len(bad) > 0 {
		return nil, invalid("invalid values; nothing was saved", bad)
	}
	return values, nil
}
