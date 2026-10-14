package deploynet

import (
	"errors"
	"testing"
)

func reset(t *testing.T) {
	t.Helper()
	current.Store(nil)
	t.Cleanup(func() { current.Store(nil) })
}

func TestResolveUnset(t *testing.T) {
	reset(t)
	cases := map[string]string{"": Testnet, "testnet": Testnet, " Mainnet ": Mainnet}
	for raw, want := range cases {
		got, err := Resolve(raw)
		if err != nil || got != want {
			t.Errorf("Resolve(%q) = %q, %v; want %q", raw, got, err, want)
		}
	}
}

func TestResolveDeployed(t *testing.T) {
	for _, deployed := range []string{Testnet, Mainnet} {
		reset(t)
		Set(deployed)
		other := Testnet
		if deployed == Testnet {
			other = Mainnet
		}

		for _, raw := range []string{"", deployed, " " + deployed + " "} {
			got, err := Resolve(raw)
			if err != nil || got != deployed {
				t.Errorf("%s: Resolve(%q) = %q, %v; want %q", deployed, raw, got, err, deployed)
			}
		}
		if _, err := Resolve(other); !errors.Is(err, ErrMismatch) {
			t.Errorf("%s: Resolve(%q) err = %v; want ErrMismatch", deployed, other, err)
		}
		if _, err := Resolve("futurenet"); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: Resolve(futurenet) err = %v; want ErrInvalid", deployed, err)
		}
	}
}

func TestSetRejectsInvalid(t *testing.T) {
	reset(t)
	defer func() {
		if recover() == nil {
			t.Fatal("Set(\"futurenet\") did not panic")
		}
	}()
	Set("futurenet")
}

func TestPassphrase(t *testing.T) {
	if got := Passphrase(Mainnet); got != "Public Global Stellar Network ; September 2015" {
		t.Errorf("Passphrase(mainnet) = %q", got)
	}
	if got := Passphrase(Testnet); got != "Test SDF Network ; September 2015" {
		t.Errorf("Passphrase(testnet) = %q", got)
	}
}
