package auth

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestHashAndVerify(t *testing.T) {
	hash, err := HashPassword("Correct-Horse-7!")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hash, "$argon2id$") {
		t.Errorf("hash should describe itself, got %q", hash)
	}

	if ok, err := VerifyPassword("Correct-Horse-7!", hash); err != nil || !ok {
		t.Errorf("the right password should verify: ok=%v err=%v", ok, err)
	}
	if ok, _ := VerifyPassword("Correct-Horse-8!", hash); ok {
		t.Error("a wrong password must not verify")
	}

	again, _ := HashPassword("Correct-Horse-7!")
	if again == hash {
		t.Error("the same password should hash differently each time (random salt)")
	}
}

func TestVerifyRejectsDamagedHashes(t *testing.T) {
	for _, bad := range []string{"", "plaintext", "$argon2id$v=19$garbage", "$bcrypt$v=1$a$b$c"} {
		if _, err := VerifyPassword("anything", bad); !errors.Is(err, ErrBadHash) {
			t.Errorf("VerifyPassword with %q: want ErrBadHash, got %v", bad, err)
		}
	}
}

func TestUnicodeNormalization(t *testing.T) {
	// Both of these display as "Café-Kr8!", but the é is built differently:
	composed := "Caf\u00e9-Kr8!"    // é as a single character
	decomposed := "Cafe\u0301-Kr8!" // e followed by a combining accent

	if composed == decomposed {
		t.Fatal("test setup: the two strings should differ byte for byte")
	}

	hash, err := HashPassword(composed)
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := VerifyPassword(decomposed, hash); !ok {
		t.Error("the same visible password typed on another device should still verify")
	}
}

func TestPasswordProblems(t *testing.T) {
	cases := []struct {
		password string
		username string
		want     string // a phrase the problems should include; "" means it should pass
	}{
		{"Tr0ub4dor&3", "", ""},
		{"Blue-Sky-42!", "", ""},
		{"short", "", "at least 6 characters"},
		{"nouppercase1!", "", "uppercase letter"},
		{"NoNumber!", "", "a number"},
		{"NoSymbol123", "", "a symbol"},
		{"Password1!", "", "common word"},
		{"P@ssw0rd!", "", "common word"},
		{"Summer2024!", "", "common word"},
		{"!!Snowball99", "", "common word"},
		{"Blue-Humming-Otter1!", "blue-humming-otter", "your username"},
		{strings.Repeat("A1!", 50), "", "at most 128"},
	}

	for _, c := range cases {
		problems := strings.Join(PasswordProblems(c.password, c.username), "; ")
		switch {
		case c.want == "" && problems != "":
			t.Errorf("%q should pass, got: %s", c.password, problems)
		case c.want != "" && !strings.Contains(problems, c.want):
			t.Errorf("%q: want a problem mentioning %q, got: %q", c.password, c.want, problems)
		}
	}
}

func TestBusyWhenAllSlotsTaken(t *testing.T) {
	// Fill every hashing slot, as if the server were flooded.
	for range maxConcurrentHashes {
		hashSlots <- struct{}{}
	}
	defer func() {
		for range maxConcurrentHashes {
			<-hashSlots
		}
	}()

	old := busyTimeout
	busyTimeout = 50 * time.Millisecond
	defer func() { busyTimeout = old }()

	if _, err := HashPassword("Anything-1!"); !errors.Is(err, ErrBusy) {
		t.Errorf("with every slot taken, want ErrBusy, got %v", err)
	}
}
