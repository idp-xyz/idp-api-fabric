package domain

import (
	"regexp"
	"time"
	"unicode/utf8"
)

// LogoURLPattern is the pattern of where the logo of a brand is fetched
// from: an https URL of a host, by the rules of the entry of the
// documentation (DocsURLPattern).
//
// The contract of the API states the same pattern, and a test holds the two
// together.
const LogoURLPattern = DocsURLPattern

// LogoURLMaxLength bounds the URL of a logo, in characters. The contract of
// the API states the same bound.
const LogoURLMaxLength = DocsURLMaxLength

// ColorPattern is the pattern of a colour: "#" and six hexadecimal digits,
// two each for red, green and blue, in either case.
//
// The contract of the API states the same pattern, and a test holds the two
// together.
const ColorPattern = `^#[0-9A-Fa-f]{6}$`

var color = regexp.MustCompile(ColorPattern)

// Brand is how a product presents itself to the developers it documents its
// APIs for (FR-005): the name, the logo and the colour a portal of the
// product is made up with. Every product has one, and one product's says
// nothing about another's.
//
// Fabric keeps a brand and shows it to whoever may read the product. It
// renders nothing with it.
type Brand struct {
	// Name is the name the product presents itself under. A product is
	// registered with its display name for it.
	Name string
	// LogoURL is where the logo is fetched from. It is empty for as long as
	// the product has none. Fabric keeps the URL as it is given and never
	// opens it, so it cannot tell what is there.
	LogoURL string
	// PrimaryColor is the colour of the product, kept as it is given. It is
	// empty for as long as the product has none.
	PrimaryColor string
}

// check accepts what can be the brand of a product.
func (b Brand) check() error {
	if err := checkDisplayName("the name of a brand", b.Name); err != nil {
		return err
	}
	if err := checkLogoURL(b.LogoURL); err != nil {
		return err
	}
	if b.PrimaryColor != "" && !color.MatchString(b.PrimaryColor) {
		return invalid("the colour of a brand is # and six hexadecimal digits")
	}
	return nil
}

// checkLogoURL accepts where the logo of a brand is fetched from, or nothing.
// The rules are those of the entry of the documentation.
func checkLogoURL(value string) error {
	switch {
	case value == "":
		return nil
	case !utf8.ValidString(value):
		return invalid("the logo of a brand is not valid UTF-8")
	case utf8.RuneCountInString(value) > LogoURLMaxLength:
		return invalid("the logo of a brand is longer than %d characters", LogoURLMaxLength)
	case !docsURL.MatchString(value):
		return invalid("the logo of a brand must be a valid https URL without credentials or whitespace")
	}
	return nil
}

// ChangeBrand makes brand the brand of the product from the instant at, at
// the next version of the product, and says who changed it. A product whose
// brand is brand already is returned as it is: nothing changes, and its
// version stays.
//
// A brand is replaced as a whole: what brand leaves empty, the product has
// none of from then on. A suspended product takes no change (INV-01).
//
// A clock that runs behind the one of the last change does not keep the
// product from changing: the change is then at the instant of the last one.
func (p Product) ChangeBrand(brand Brand, by Principal, at time.Time) (Product, error) {
	if err := p.AdmitNewWork(); err != nil {
		return Product{}, err
	}
	if err := brand.check(); err != nil {
		return Product{}, err
	}
	if err := by.Validate(); err != nil {
		return Product{}, err
	}
	if at.IsZero() {
		return Product{}, invalid("a brand is changed at a known time")
	}
	if brand == p.Brand {
		return p, nil
	}
	at = instant(at)
	if at.Before(p.UpdatedAt) {
		at = p.UpdatedAt
	}
	p.Brand, p.UpdatedAt, p.UpdatedBy, p.Version = brand, at, by, p.Version+1
	return p, nil
}
