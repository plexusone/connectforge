package memstore

import (
	"slices"

	cf "github.com/plexusone/connectforge"
)

// CredentialVersions returns the stored credential versions for a
// connection, in order.
func CredentialVersions(s *Store, owner cf.OwnerID, id string) []int {
	s.mu.Lock()
	defer s.mu.Unlock()
	set := s.creds[connKey{owner, id}]
	if set == nil {
		return nil
	}
	out := make([]int, 0, len(set.versions))
	for v := range set.versions {
		out = append(out, v)
	}
	slices.Sort(out)
	return out
}
