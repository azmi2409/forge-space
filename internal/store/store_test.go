package store

import (
	"strings"
	"testing"
	"time"
)

func TestNewIDFormat(t *testing.T) {
	for prefix, want := range map[string]string{
		PrefixSpace: "sp_", PrefixMap: "mp_", PrefixParticipant: "pt_",
		PrefixObject: "ob_", PrefixArea: "ar_", PrefixAgentKey: "ak_",
		PrefixUser: "us_", PrefixTicket: "tk_",
	} {
		_ = want
		id := NewID(prefix)
		if !strings.HasPrefix(id, prefix) {
			t.Fatalf("id %q missing prefix %q", id, prefix)
		}
		rest := id[len(prefix):]
		if len(rest) != 16 {
			t.Fatalf("id %q random part len = %d, want 16", id, len(rest))
		}
		for _, r := range rest {
			if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'z') {
				t.Fatalf("id %q not base36", id)
			}
		}
	}
	// uniqueness
	seen := map[string]bool{}
	for i := 0; i < 1000; i++ {
		id := NewID(PrefixParticipant)
		if seen[id] {
			t.Fatal("duplicate ID generated")
		}
		seen[id] = true
	}
}

func openTest(t *testing.T) *Store {
	t.Helper()
	st, err := Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestTicketClaimSingleUse(t *testing.T) {
	st := openTest(t)
	tid := NewID(PrefixTicket)
	exp := time.Now().UTC().Add(time.Minute).Format(time.RFC3339)
	// participant FK: create minimal space/user/participant chain
	u, _ := st.CreateUser(NewID(PrefixUser), "a@x.com", "hash", "A", Now())
	sp := &Space{ID: NewID(PrefixSpace), Name: "S", Slug: "s1", OwnerUserID: u.ID,
		Settings: SpaceSettings{ProximityRadius: 5}, CreatedAt: Now()}
	if err := st.CreateSpace(sp); err != nil {
		t.Fatal(err)
	}
	m := &Map{ID: NewID(PrefixMap), SpaceID: sp.ID, Name: "M", Width: 10, Height: 10,
		Tiles: make([]int, 100), Spawn: Spawn{X: 5, Y: 5}, CreatedAt: Now()}
	if err := st.CreateMap(m); err != nil {
		t.Fatal(err)
	}
	p := &Participant{ID: NewID(PrefixParticipant), SpaceID: sp.ID, MapID: m.ID,
		Kind: "human", DisplayName: "A", X: 5, Y: 5, Dir: "down",
		OwnerUserID: u.ID, CreatedAt: Now(), UpdatedAt: Now()}
	if err := st.CreateParticipant(p); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateTicket(tid, p.ID, "hash123", exp, Now()); err != nil {
		t.Fatal(err)
	}
	ok, err := st.ClaimTicket(tid)
	if err != nil || !ok {
		t.Fatalf("first claim = %v,%v, want true,nil", ok, err)
	}
	ok, err = st.ClaimTicket(tid)
	if err != nil || ok {
		t.Fatalf("second claim = %v,%v, want false,nil", ok, err)
	}
}

func TestChatMessageRoundtrip(t *testing.T) {
	st := openTest(t)
	u, _ := st.CreateUser(NewID(PrefixUser), "a@x.com", "hash", "A", Now())
	sp := &Space{ID: NewID(PrefixSpace), Name: "S", Slug: "s2", OwnerUserID: u.ID,
		Settings: SpaceSettings{ProximityRadius: 5}, CreatedAt: Now()}
	_ = st.CreateSpace(sp)
	msg := &ChatMessage{
		ID: NewID(PrefixChatMessage), SpaceID: sp.ID,
		FromParticipantID: "pt_x", FromName: "A", FromKind: "human",
		Scope: "nearby", Text: "hi", Recipients: []string{"pt_y"}, CreatedAt: Now(),
	}
	if err := st.CreateChatMessage(msg); err != nil {
		t.Fatal(err)
	}
	msgs, err := st.ListChatMessages(sp.ID, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 || msgs[0].Text != "hi" {
		t.Fatalf("got %v", msgs)
	}
	if len(msgs[0].Recipients) != 1 || msgs[0].Recipients[0] != "pt_y" {
		t.Fatalf("recipients = %v", msgs[0].Recipients)
	}
}
