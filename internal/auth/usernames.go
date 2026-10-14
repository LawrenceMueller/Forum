package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	mrand "math/rand/v2"
)

// Names look like "blue-humming-otter": color, verb, animal.
// 8 x 80 x 150 = 96,000 possible names.
//
// Hand-curated so random combinations stay friendly. Before adding a word,
// read it next to every color and a few random words from the other list.
// Adding words is always safe; existing names are unaffected.

var nameColors = []string{
	"blue", "green", "red", "pink", "orange", "yellow", "brown", "purple",
}

var nameVerbs = []string{
	"dancing", "singing", "humming", "whistling", "wandering", "drifting", "sleeping", "dreaming", "reading", "painting",
	"baking", "knitting", "skating", "sledding", "skiing", "hiking", "climbing", "swimming", "paddling", "sailing",
	"rowing", "fishing", "gardening", "juggling", "laughing", "giggling", "grinning", "waving", "winking", "napping",
	"snoozing", "yawning", "stretching", "hopping", "skipping", "bouncing", "tumbling", "rolling", "spinning", "twirling",
	"gliding", "soaring", "floating", "flying", "running", "racing", "strolling", "tiptoeing", "peeking", "pondering",
	"wondering", "thinking", "scribbling", "sketching", "doodling", "writing", "counting", "fixing", "tinkering", "building",
	"cooking", "brewing", "sipping", "munching", "snacking", "foraging", "digging", "burrowing", "nesting", "roaming",
	"exploring", "drumming", "strumming", "rhyming", "joking", "sneezing", "blinking", "shivering", "jogging", "waddling",
}

var nameAnimals = []string{
	"otter", "heron", "wren", "owl", "fox", "marten", "badger", "hare", "lark", "moose",
	"puffin", "raven", "robin", "walrus", "yak", "finch", "sparrow", "starling", "magpie", "jay",
	"crane", "stork", "pelican", "penguin", "albatross", "gull", "tern", "loon", "grebe", "duck",
	"goose", "swan", "mallard", "kingfisher", "woodpecker", "hummingbird", "parrot", "macaw", "toucan", "flamingo",
	"ibis", "egret", "osprey", "falcon", "hawk", "eagle", "kestrel", "quail", "pheasant", "grouse",
	"ptarmigan", "dove", "pigeon", "cardinal", "bluebird", "chickadee", "nuthatch", "warbler", "oriole", "bear",
	"wolf", "lynx", "bobcat", "ermine", "stoat", "mink", "ferret", "mole", "vole", "hedgehog",
	"porcupine", "squirrel", "chipmunk", "marmot", "lemming", "pika", "rabbit", "deer", "elk", "caribou",
	"reindeer", "antelope", "gazelle", "ibex", "bison", "camel", "llama", "alpaca", "zebra", "giraffe",
	"elephant", "rhino", "tapir", "okapi", "panda", "koala", "wombat", "wallaby", "kangaroo", "platypus",
	"echidna", "sloth", "armadillo", "anteater", "aardvark", "meerkat", "mongoose", "tiger", "lion", "leopard",
	"jaguar", "cheetah", "ocelot", "caracal", "serval", "fennec", "coyote", "narwhal", "dolphin", "orca",
	"manatee", "beluga", "turtle", "tortoise", "gecko", "iguana", "chameleon", "newt", "salamander", "axolotl",
	"frog", "toad", "salmon", "trout", "minnow", "guppy", "pufferfish", "seahorse", "octopus", "squid",
	"jellyfish", "starfish", "crab", "lobster", "shrimp", "bee", "bumblebee", "beetle", "ladybug", "firefly",
}

// maxNameAttempts bounds the retry loop. If this many random picks are all
// taken, the lists are nearly full and need more words.
const maxNameAttempts = 100

var ErrNamesExhausted = errors.New("could not find an unused name; add more words to the lists")

// RandomName builds one name, using pick to choose a position in each word
// list (pick(n) returns a number from 0 to n-1). Sign-up passes math/rand's
// IntN; the seed program passes its own seeded generator, so it can build
// the same names every run.
func RandomName(pick func(n int) int) string {
	return fmt.Sprintf("%s-%s-%s",
		nameColors[pick(len(nameColors))],
		nameVerbs[pick(len(nameVerbs))],
		nameAnimals[pick(len(nameAnimals))],
	)
}

// GenerateUsername picks random names until taken reports one is free.
// taken can fail (it usually asks the database), and that error is passed along.
func GenerateUsername(taken func(string) (bool, error)) (string, error) {
	for range maxNameAttempts {
		name := RandomName(mrand.IntN)
		used, err := taken(name)
		if err != nil {
			return "", err
		}
		if !used {
			return name, nil
		}
	}
	return "", ErrNamesExhausted
}

// NameSigner proves a name was offered by this server. Offered names travel
// through the browser, so without a signature anyone could edit the form
// (or the cookie) and choose their own.
type NameSigner struct {
	key []byte
}

// NewNameSigner makes a signer with a random secret. Restarting the server
// makes a new one, which invalidates names offered before the restart;
// that's harmless, the visitor just gets a fresh name.
func NewNameSigner() (*NameSigner, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	return &NameSigner{key: key}, nil
}

func (n *NameSigner) Sign(name string) string {
	mac := hmac.New(sha256.New, n.key)
	mac.Write([]byte(name))
	return hex.EncodeToString(mac.Sum(nil))
}

func (n *NameSigner) Valid(name, sig string) bool {
	// hmac.Equal takes the same time whether the first byte or the last
	// byte differs, so timing can't leak how close a guess was.
	return hmac.Equal([]byte(n.Sign(name)), []byte(sig))
}
