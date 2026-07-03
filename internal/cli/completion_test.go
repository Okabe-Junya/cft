package cli

import (
	"errors"
	"reflect"
	"testing"

	"github.com/Okabe-Junya/cft/internal/store"
	"github.com/spf13/cobra"
)

// fixtureLoader returns an indexLoader serving a two-profile index: "default"
// with two tokens and "work" with one.
func fixtureLoader() indexLoader {
	return func() (*store.Index, error) {
		idx := store.NewIndex()
		idx.Current = store.DefaultProfile
		d := idx.Profile(store.DefaultProfile)
		d.Set("dns-editor-example-com", store.Entry{ID: "aaa"})
		d.Set("cache-purge-example-com", store.Entry{ID: "bbb"})
		idx.Profile("work").Set("zone-reader-work-com", store.Entry{ID: "ccc"})
		return idx, nil
	}
}

// profileCmd builds a command carrying the --profile persistent flag set to
// val, mirroring how the real root wires it, so selectedProfile can resolve it.
func profileCmd(t *testing.T, val string) *cobra.Command {
	t.Helper()
	t.Setenv(EnvProfile, "") // keep $CFT_PROFILE out of the resolution
	cmd := &cobra.Command{}
	cmd.Flags().String("profile", "", "")
	if val != "" {
		if err := cmd.Flags().Set("profile", val); err != nil {
			t.Fatal(err)
		}
	}
	return cmd
}

func TestCompleteTokenNames(t *testing.T) {
	tests := []struct {
		name    string
		profile string
		args    []string
		toComp  string
		want    []string
		wantDir cobra.ShellCompDirective
	}{
		{
			name:    "default profile, no prefix, sorted",
			args:    nil,
			toComp:  "",
			want:    []string{"cache-purge-example-com", "dns-editor-example-com"},
			wantDir: cobra.ShellCompDirectiveNoFileComp,
		},
		{
			name:    "prefix filters",
			toComp:  "dns",
			want:    []string{"dns-editor-example-com"},
			wantDir: cobra.ShellCompDirectiveNoFileComp,
		},
		{
			name:    "selected profile scopes candidates",
			profile: "work",
			toComp:  "",
			want:    []string{"zone-reader-work-com"},
			wantDir: cobra.ShellCompDirectiveNoFileComp,
		},
		{
			name:    "second argument falls back to default completion",
			args:    []string{"dns-editor-example-com"},
			toComp:  "",
			want:    nil,
			wantDir: cobra.ShellCompDirectiveDefault,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, dir := completeTokenNames(fixtureLoader())(profileCmd(t, tt.profile), tt.args, tt.toComp)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("candidates = %v, want %v", got, tt.want)
			}
			if dir != tt.wantDir {
				t.Errorf("directive = %v, want %v", dir, tt.wantDir)
			}
		})
	}
}

func TestCompleteTokenNames_LoadErrorOffersNothing(t *testing.T) {
	load := func() (*store.Index, error) { return nil, errors.New("boom") }
	got, dir := completeTokenNames(load)(profileCmd(t, ""), nil, "")
	if got != nil {
		t.Errorf("candidates = %v, want nil", got)
	}
	if dir != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("directive = %v, want NoFileComp", dir)
	}
}

func TestCompleteProfileNames(t *testing.T) {
	got, dir := completeProfileNames(fixtureLoader())(profileCmd(t, ""), nil, "")
	want := []string{"default", "work"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("candidates = %v, want %v", got, want)
	}
	if dir != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("directive = %v, want NoFileComp", dir)
	}

	// Once the argument is present, offer nothing further.
	got, _ = completeProfileNames(fixtureLoader())(profileCmd(t, ""), []string{"default"}, "")
	if got != nil {
		t.Errorf("candidates after first arg = %v, want nil", got)
	}
}
