package peers

import (
	"slices"
	"testing"
)

func names(s *Store) []string {
	var out []string
	for _, p := range s.List() {
		out = append(out, p.Name)
	}
	return out
}

// The user's order survives reopening; peers added later join at the end.
func TestReorder(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir)
	for _, n := range []string{"Cedar", "Aloe", "Banyan"} {
		if err := s.Add(Peer{ID: n, Name: n}); err != nil {
			t.Fatal(err)
		}
	}
	if got := names(s); !slices.Equal(got, []string{"Aloe", "Banyan", "Cedar"}) {
		t.Fatalf("by name: %v", got)
	}
	if err := s.Reorder([]string{"Cedar", "Aloe"}); err != nil {
		t.Fatal(err)
	}
	s.Add(Peer{ID: "Agave", Name: "Agave"})
	s.Add(Peer{ID: "Aloe", Name: "Aloe"}) // re-pairing keeps the place
	s, _ = Open(dir)
	if got := names(s); !slices.Equal(got, []string{"Cedar", "Aloe", "Banyan", "Agave"}) {
		t.Fatalf("after reorder: %v", got)
	}
}
