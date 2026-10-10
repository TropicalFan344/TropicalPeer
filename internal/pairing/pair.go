package pairing

import (
	"context"
	"crypto/ed25519"
	"crypto/hkdf"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"filippo.io/cpace"
	"golang.org/x/crypto/chacha20poly1305"

	"TropicalPeer/internal/identity"
	"TropicalPeer/internal/rendezvous"
)

// exchangeTimeout bounds the PAKE once both devices are connected.
const exchangeTimeout = time.Minute

// ErrWrongCode means the two devices did not use the same code.
var ErrWrongCode = errors.New("pairing failed: the code did not match")

// Result describes the device we just paired with.
type Result struct {
	ID        string
	Name      string
	PublicKey ed25519.PublicKey
}

// wire is the only message type exchanged through the server during pairing.
type wire struct {
	Pake []byte `json:"pake,omitempty"`
	Box  []byte `json:"box,omitempty"`
}

// card is what each device reveals about itself, encrypted under the PAKE key.
type card struct {
	Name      string            `json:"name"`
	PublicKey ed25519.PublicKey `json:"publicKey"`
}

// Invite creates a pairing code. onCode is called with the code to show to
// the user; Invite then blocks until the other device joins and pairing ends.
func Invite(ctx context.Context, rc *rendezvous.Client, onCode func(code string)) (*Result, error) {
	sess, err := rc.CreatePair(ctx)
	if err != nil {
		return nil, err
	}
	defer sess.Close()

	code, err := FormatCode(sess.Nameplate)
	if err != nil {
		return nil, err
	}
	onCode(code)

	if err := sess.WaitForPeer(ctx); err != nil {
		return nil, err
	}
	stop := armDeadline(ctx, sess)
	defer stop()

	msgA, state, err := cpace.Start(code, contextInfo(sess.Nameplate))
	if err != nil {
		return nil, err
	}
	if err := sess.Send(wire{Pake: msgA}); err != nil {
		return nil, err
	}

	var in wire
	if err := sess.Recv(&in); err != nil {
		return nil, fmt.Errorf("other device left: %w", err)
	}
	key, err := state.Finish(in.Pake)
	if err != nil {
		return nil, ErrWrongCode
	}
	peer, err := openCard(key, "joiner", in.Box)
	if err != nil {
		return nil, err
	}

	box, err := sealCard(key, "inviter", rc.ID)
	if err != nil {
		return nil, err
	}
	if err := sess.Send(wire{Box: box}); err != nil {
		return nil, err
	}
	return peer, nil
}

// Join pairs with the device that showed the given code.
func Join(ctx context.Context, rc *rendezvous.Client, input string) (*Result, error) {
	nameplate, code, err := ParseCode(input)
	if err != nil {
		return nil, err
	}
	sess, err := rc.JoinPair(ctx, nameplate)
	if err != nil {
		return nil, err
	}
	defer sess.Close()
	stop := armDeadline(ctx, sess)
	defer stop()

	var in wire
	if err := sess.Recv(&in); err != nil {
		return nil, fmt.Errorf("other device left: %w", err)
	}
	msgB, key, err := cpace.Exchange(code, contextInfo(nameplate), in.Pake)
	if err != nil {
		return nil, ErrWrongCode
	}
	box, err := sealCard(key, "joiner", rc.ID)
	if err != nil {
		return nil, err
	}
	if err := sess.Send(wire{Pake: msgB, Box: box}); err != nil {
		return nil, err
	}

	if err := sess.Recv(&in); err != nil {
		// The inviter hangs up without replying when the code was wrong.
		return nil, ErrWrongCode
	}
	return openCard(key, "inviter", in.Box)
}

func contextInfo(nameplate string) *cpace.ContextInfo {
	return cpace.NewContextInfo("tropicalpeer-inviter", "tropicalpeer-joiner", []byte("tropicalpeer-pair/1 "+nameplate))
}

func armDeadline(ctx context.Context, sess *rendezvous.PairSession) func() bool {
	sess.SetDeadline(time.Now().Add(exchangeTimeout))
	return context.AfterFunc(ctx, func() { sess.SetDeadline(time.Now()) })
}

// boxKey derives a separate one-time key per direction from the PAKE output.
func boxKey(pakeKey []byte, sender string) ([]byte, error) {
	return hkdf.Key(sha256.New, pakeKey, nil, "tropicalpeer card from "+sender, chacha20poly1305.KeySize)
}

func sealCard(pakeKey []byte, sender string, id *identity.Identity) ([]byte, error) {
	k, err := boxKey(pakeKey, sender)
	if err != nil {
		return nil, err
	}
	aead, err := chacha20poly1305.New(k)
	if err != nil {
		return nil, err
	}
	plain, err := json.Marshal(card{Name: id.Name, PublicKey: id.PublicKey})
	if err != nil {
		return nil, err
	}
	// Each key encrypts exactly one message, so a zero nonce is safe.
	return aead.Seal(nil, make([]byte, aead.NonceSize()), plain, nil), nil
}

func openCard(pakeKey []byte, sender string, box []byte) (*Result, error) {
	k, err := boxKey(pakeKey, sender)
	if err != nil {
		return nil, err
	}
	aead, err := chacha20poly1305.New(k)
	if err != nil {
		return nil, err
	}
	plain, err := aead.Open(nil, make([]byte, aead.NonceSize()), box, nil)
	if err != nil {
		return nil, ErrWrongCode
	}
	var c card
	if err := json.Unmarshal(plain, &c); err != nil {
		return nil, err
	}
	if len(c.PublicKey) != ed25519.PublicKeySize {
		return nil, errors.New("peer sent an invalid public key")
	}
	return &Result{ID: identity.DeviceID(c.PublicKey), Name: c.Name, PublicKey: c.PublicKey}, nil
}
