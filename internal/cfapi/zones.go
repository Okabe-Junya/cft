package cfapi

import (
	"context"
	"fmt"

	"github.com/cloudflare/cloudflare-go/v7"
	"github.com/cloudflare/cloudflare-go/v7/zones"
)

// Zone is the trimmed Cloudflare zone record we need (just id + name).
type Zone struct {
	ID   string
	Name string
}

// ResolveZoneID returns the zone ID for the given exact zone name. The
// /zones endpoint's `name` query is documented to match exactly (no
// substring), but the API has historically widened that filter via
// `name.contains` and the auto-paginating iterator will happily return any
// near-match. We post-filter for `Name == name` so the result is
// deterministic.
func (c *Client) ResolveZoneID(ctx context.Context, name string) (string, error) {
	iter := c.cf.Zones.ListAutoPaging(ctx, zones.ZoneListParams{
		Name: cloudflare.F(name),
	})
	var matches []zones.Zone
	for iter.Next() {
		z := iter.Current()
		if z.Name == name {
			matches = append(matches, z)
		}
	}
	if err := iter.Err(); err != nil {
		return "", mapError(err)
	}
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("cfapi: zone %q not found", name)
	case 1:
		return matches[0].ID, nil
	default:
		return "", fmt.Errorf("cfapi: zone %q is ambiguous (%d matches)", name, len(matches))
	}
}
