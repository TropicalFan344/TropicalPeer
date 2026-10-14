// Package pairing turns a short code like "7-orchid-tiger" into mutual trust
// between two devices. The number ("nameplate") lets the rendezvous server
// connect the two devices; the words are the secret, fed into a PAKE (CPace)
// so an eavesdropper or the server itself gets a single guess at most.
package pairing

import (
	"crypto/rand"
	"errors"
	"math/big"
	"strings"
)

// wordCount is the number of secret words in a code. With the list below that
// is about 16 bits per code; an attacker gets exactly one online guess before
// the single-use nameplate is consumed.
const wordCount = 2

// FormatCode builds a full code for a nameplate by drawing random words.
func FormatCode(nameplate string) (string, error) {
	parts := []string{nameplate}
	for range wordCount {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(words))))
		if err != nil {
			return "", err
		}
		parts = append(parts, words[n.Int64()])
	}
	return strings.Join(parts, "-"), nil
}

// ParseCode normalises user input ("7 Orchid tiger", " 7-orchid-tiger ") and
// returns the nameplate and the normalised full code used as the PAKE password.
func ParseCode(input string) (nameplate, code string, err error) {
	fields := strings.FieldsFunc(strings.ToLower(input), func(r rune) bool {
		return r == '-' || r == ' ' || r == '_' || r == '.'
	})
	if len(fields) != 1+wordCount {
		return "", "", errors.New("a pairing code looks like 7-orchid-tiger")
	}
	for _, r := range fields[0] {
		if r < '0' || r > '9' {
			return "", "", errors.New("a pairing code starts with a number, like 7-orchid-tiger")
		}
	}
	return fields[0], strings.Join(fields, "-"), nil
}

var words = strings.Fields(`
	acorn agave almond amber anchor apple apricot arch aspen atlas aurora autumn
	badger bamboo banana banyan basil bay beach beacon bear berry birch bison
	blaze bloom blossom boat bongo breeze brook bubble cactus calm camel canoe
	canyon cape cashew cedar cherry citrus clam cliff cloud clover cobalt coconut
	comet conch coral cosmos cove coyote crab crane creek cricket cypress dahlia
	daisy delta desert dew dingo dolphin dove dragon drift dune eagle echo
	eclipse ember emerald falcon fennel fern fig finch firefly fjord flamingo
	flint forest fox frost galaxy gecko geyser ginger glacier glade gorilla grape
	grove guava gull harbor hawk hazel heron hibiscus hill honey horizon hummingbird
	iguana indigo iris island ivory ivy jade jaguar jasmine jelly jungle juniper
	kayak kelp kestrel kiwi koala lagoon lake lark lava lemon leopard lily
	lime lion lotus lunar lychee lynx macaw mango mangrove maple marble marina
	marsh meadow melon mesa meteor mint mist monsoon moon moss mountain nectar
	nebula nutmeg oasis ocean olive onyx orange orbit orca orchid osprey otter
	owl oyster palm panda papaya parrot peach peak pearl pebble pelican pepper
	petal pine plum polar pollen pond poppy prairie puffin puma quartz quail
	rain raven reef ridge river robin rose ruby saffron sage salmon sand
	sapphire savanna seal shell shore sierra sky sloth snow solar sparrow spice
	spruce squid star stone storm summit sun swan tamarind tango tapir tea
	thunder tide tiger toucan trail tulip tundra turtle twilight valley vanilla
	velvet violet volcano walnut walrus wave whale willow wind wolf yak zebra
`)

// RandomName returns two capitalised words from the code word list, like
// "Orchid Tiger", for naming someone who has no device name of their own.
func RandomName() string {
	parts := make([]string, 2)
	for i := range parts {
		n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(words))))
		w := words[n.Int64()]
		parts[i] = strings.ToUpper(w[:1]) + w[1:]
	}
	return strings.Join(parts, " ")
}
