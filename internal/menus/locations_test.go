package menus

import (
	"context"
	"reflect"
	"testing"

	"github.com/zadstack/gorun/internal/kernel"
)

type fakeInfo struct{ Locations []string }
type fakeTheme struct{ locs []string }

func (f fakeTheme) Active(context.Context) fakeInfo { return fakeInfo{Locations: f.locs} }

func TestThemeLocations(t *testing.T) {
	d := &kernel.Deps{Services: kernel.NewServiceRegistry()}
	ctx := context.Background()
	if got := themeLocations(ctx, d); !reflect.DeepEqual(got, []string{"primary", "footer"}) {
		t.Fatalf("default: %v", got)
	}
	d.Services.Set(kernel.ServiceTheme, fakeTheme{locs: []string{"header", "Bad Name", "social"}})
	if got := themeLocations(ctx, d); !reflect.DeepEqual(got, []string{"header", "social"}) {
		t.Fatalf("theme: %v", got)
	}
	d.Services.Set(kernel.ServiceTheme, fakeTheme{})
	if got := themeLocations(ctx, d); !reflect.DeepEqual(got, DefaultLocations) {
		t.Fatalf("empty theme locations: %v", got)
	}
	d.Services.Set(kernel.ServiceTheme, "not a theme")
	if got := themeLocations(ctx, d); !reflect.DeepEqual(got, DefaultLocations) {
		t.Fatalf("wrong type: %v", got)
	}
}
