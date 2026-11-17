package domain

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// developers is the brand CargoPai gives itself: a name of its own, a logo
// and a colour.
var developers = Brand{
	Name:         "CargoPai Developers",
	LogoURL:      "https://static.cargopai.example/brand/logo.svg",
	PrimaryColor: "#0A7B3C",
}

// FR-005: a product has a brand from its registration. Until an owner
// changes it, the product presents itself under its display name, without a
// logo and without a colour.
func TestAProductIsRegisteredWithItsDisplayNameForABrand(t *testing.T) {
	product := registered(t)

	if want := (Brand{Name: "CargoPai"}); product.Brand != want {
		t.Errorf("the brand is %+v, want %+v", product.Brand, want)
	}
	if product.UpdatedBy != admin || product.UpdatedAt != product.CreatedAt {
		t.Errorf("the product was last changed by %+v at %s, want by its registrant when it was registered", product.UpdatedBy, product.UpdatedAt)
	}
}

func TestAChangeOfTheBrandSaysWhoChangedItAndWhen(t *testing.T) {
	product := registered(t)
	at := time.Date(2026, 11, 17, 9, 30, 0, 987654321, time.FixedZone("UTC+8", 8*60*60))

	changed, err := product.ChangeBrand(developers, alice, at)
	if err != nil {
		t.Fatalf("change the brand: %v", err)
	}
	want := product
	want.Brand, want.Version = developers, 2
	want.UpdatedAt, want.UpdatedBy = time.Date(2026, 11, 17, 1, 30, 0, 987654000, time.UTC), alice
	if changed != want {
		t.Errorf("changed\n %+v\nwant\n %+v", changed, want)
	}
	if changed.UpdatedAt.Location() != time.UTC {
		t.Errorf("the change is timed in %s, want UTC", changed.UpdatedAt.Location())
	}
	// Changing returns the product as it is from then on. The one it was
	// called on is a value and stays what it was.
	if product.Brand != (Brand{Name: "CargoPai"}) || product.Version != 1 || product.UpdatedBy != admin {
		t.Errorf("the product that was changed changed in place: %+v", product)
	}

	// A brand is replaced as a whole: what the new one leaves out, the
	// product has none of from then on.
	plain, err := changed.ChangeBrand(Brand{Name: "CargoPai 开放平台"}, bob, at.Add(time.Hour))
	if err != nil {
		t.Fatalf("change the brand again: %v", err)
	}
	if plain.Brand != (Brand{Name: "CargoPai 开放平台"}) || plain.Version != 3 || plain.UpdatedBy != bob {
		t.Errorf("changed again %+v, want the name alone at version 3, by bob", plain)
	}
	// Nothing else of the product moves with its brand.
	plain.Brand, plain.Version, plain.UpdatedAt, plain.UpdatedBy = product.Brand, product.Version, product.UpdatedAt, product.UpdatedBy
	if plain != product {
		t.Errorf("a change of the brand changed more of the product than that:\n %+v\nwant, the brand aside,\n %+v", plain, product)
	}
}

// Setting the brand to what it is already changes nothing: not the version,
// and not who changed the product last.
func TestTheSameBrandAgainIsNoChange(t *testing.T) {
	product := registered(t)

	same, err := product.ChangeBrand(Brand{Name: "CargoPai"}, alice, created.Add(time.Hour))
	if err != nil {
		t.Fatalf("change the brand: %v", err)
	}
	if same != product {
		t.Errorf("got\n %+v\nwant it as it was\n %+v", same, product)
	}

	changed, err := product.ChangeBrand(developers, alice, created.Add(time.Hour))
	if err != nil {
		t.Fatalf("change the brand: %v", err)
	}
	if again, err := changed.ChangeBrand(developers, bob, created.Add(2*time.Hour)); err != nil || again != changed {
		t.Errorf("the same brand again: got %+v and %v, want the product as it was", again, err)
	}

	// A colour is kept as it is given, so the same colour in another case is
	// a change of the brand.
	lower := developers
	lower.PrimaryColor = strings.ToLower(developers.PrimaryColor)
	recoloured, err := changed.ChangeBrand(lower, bob, created.Add(2*time.Hour))
	if err != nil || recoloured.Version != 3 || recoloured.Brand.PrimaryColor != "#0a7b3c" || recoloured.UpdatedBy != bob {
		t.Errorf("the colour in lower case: got %+v and %v, want it kept as it was given, at version 3", recoloured, err)
	}
}

// The name of a brand is for people, as a display name is, and is held to
// the same rules.
func TestTheNameOfABrand(t *testing.T) {
	product := registered(t)

	accepted := []string{"C", "CargoPai Developers", "IDP 跨境小包 开放平台", "Cargo Pai (EU)", strings.Repeat("名", DisplayNameMaxLength)}
	for _, name := range accepted {
		if _, err := product.ChangeBrand(Brand{Name: name}, alice, created); err != nil {
			t.Errorf("the name %q was refused: %v", name, err)
		}
	}

	refused := map[string]string{
		"empty":            "",
		"too long":         strings.Repeat("名", DisplayNameMaxLength+1),
		"a leading space":  " CargoPai Developers",
		"a trailing space": "CargoPai Developers ",
		"only spaces":      "   ",
		"a line break":     "CargoPai\nDevelopers",
		"a tab":            "CargoPai\tDevelopers",
		"not UTF-8":        "CargoPai\xffDevelopers",
	}
	for what, name := range refused {
		t.Run(what, func(t *testing.T) {
			if changed, err := product.ChangeBrand(Brand{Name: name}, alice, created); !errors.Is(err, ErrInvalid) || changed != (Product{}) {
				t.Fatalf("got %+v and %v, want nothing and %v", changed, err, ErrInvalid)
			}
		})
	}
}

// The logo of a brand is fetched from an https URL of a host, by the rules
// of the entry of the documentation, or the brand has none.
func TestTheLogoOfABrand(t *testing.T) {
	product := registered(t)
	const prefix = "https://static.cargopai.example/"
	longest := prefix + strings.Repeat("文", LogoURLMaxLength-len(prefix))

	accepted := []string{
		"",
		"https://static.cargopai.example/brand/logo.svg",
		"https://static.cargopai.example:8443/brand/logo.png?v=3#mark",
		"https://Static.CargoPai.example/Logo.SVG",
		"https://static.cargopai.example/brand/logo%20dark.svg",
		"https://10.20.30.40/logo.png",
		longest,
	}
	for _, logo := range accepted {
		if _, err := product.ChangeBrand(Brand{Name: "CargoPai Developers", LogoURL: logo}, alice, created); err != nil {
			t.Errorf("the logo %q was refused: %v", logo, err)
		}
	}

	refused := map[string]string{
		"plain http":               "http://static.cargopai.example/logo.svg",
		"an image in the URL":      "data:image/svg+xml;base64,PHN2Zy8+",
		"a script":                 "javascript:alert(1)",
		"a path":                   "/brand/logo.svg",
		"relative to the scheme":   "//static.cargopai.example/logo.svg",
		"credentials":              "https://alice:secret@static.cargopai.example/logo.svg",
		"a host that is not ASCII": "https://静态.cargopai.example/logo.svg",
		"a port out of range":      "https://static.cargopai.example:65536/logo.svg",
		"an escape that is none":   "https://static.cargopai.example/logo%zz.svg",
		"a space":                  "https://static.cargopai.example/logo dark.svg",
		"a line break":             "https://static.cargopai.example/logo.svg\nX-Injected: 1",
		"a space in front":         " https://static.cargopai.example/logo.svg",
		"too long":                 longest + "x",
		"not UTF-8":                "https://static.cargopai.example/\xff.svg",
	}
	for what, logo := range refused {
		t.Run(what, func(t *testing.T) {
			if changed, err := product.ChangeBrand(Brand{Name: "CargoPai Developers", LogoURL: logo}, alice, created); !errors.Is(err, ErrInvalid) || changed != (Product{}) {
				t.Fatalf("got %+v and %v, want nothing and %v", changed, err, ErrInvalid)
			}
		})
	}
}

// The colour of a brand is "#" and six hexadecimal digits, or the brand has
// none.
func TestTheColourOfABrand(t *testing.T) {
	product := registered(t)

	for _, colour := range []string{"", "#0A7B3C", "#0a7b3c", "#0A7b3c", "#000000", "#FFFFFF", "#123456"} {
		changed, err := product.ChangeBrand(Brand{Name: "CargoPai Developers", PrimaryColor: colour}, alice, created)
		if err != nil || changed.Brand.PrimaryColor != colour {
			t.Errorf("the colour %q: got %+v and %v, want it kept as it is given", colour, changed.Brand, err)
		}
	}

	refused := map[string]string{
		"no number sign":       "0A7B3C",
		"three digits":         "#0A7",
		"eight digits":         "#0A7B3CFF",
		"five digits":          "#0A7B3",
		"a digit that is none": "#0A7B3G",
		"a name":               "green",
		"a function":           "rgb(10, 123, 60)",
		"a space in front":     " #0A7B3C",
		"a space behind":       "#0A7B3C ",
		"a line break behind":  "#0A7B3C\n",
		"two colours":          "#0A7B3C#FFFFFF",
		"a full-width sign":    "＃0A7B3C",
		"only the number sign": "#",
	}
	for what, colour := range refused {
		t.Run(what, func(t *testing.T) {
			if changed, err := product.ChangeBrand(Brand{Name: "CargoPai Developers", PrimaryColor: colour}, alice, created); !errors.Is(err, ErrInvalid) || changed != (Product{}) {
				t.Fatalf("got %+v and %v, want nothing and %v", changed, err, ErrInvalid)
			}
		})
	}
}

// INV-01: a suspended product takes no change of its brand, not even to the
// brand it has.
func TestASuspendedProductTakesNoChangeOfItsBrand(t *testing.T) {
	product := registered(t)
	product.Status = ProductSuspended

	for what, brand := range map[string]Brand{"another brand": developers, "the brand it has": product.Brand} {
		if changed, err := product.ChangeBrand(brand, alice, created.Add(time.Hour)); !errors.Is(err, ErrProductNotActive) || changed != (Product{}) {
			t.Errorf("%s: got %+v and %v, want nothing and %v", what, changed, err, ErrProductNotActive)
		}
	}
}

func TestAChangeOfTheBrandNeedsSomebodyAndATime(t *testing.T) {
	product := registered(t)

	cases := map[string]func() (Product, error){
		"nobody": func() (Product, error) {
			return product.ChangeBrand(developers, Principal{}, created.Add(time.Hour))
		},
		"somebody without a subject": func() (Product, error) {
			return product.ChangeBrand(developers, Principal{Issuer: alice.Issuer}, created.Add(time.Hour))
		},
		"no time": func() (Product, error) {
			return product.ChangeBrand(developers, alice, time.Time{})
		},
	}
	for what, change := range cases {
		t.Run(what, func(t *testing.T) {
			if changed, err := change(); !errors.Is(err, ErrInvalid) || changed != (Product{}) {
				t.Fatalf("got %+v and %v, want nothing and %v", changed, err, ErrInvalid)
			}
		})
	}
}

// A change is never refused for the time it carries. A process whose clock
// runs behind the one that changed the product last, or behind the one that
// registered it, changes it at the instant of that change.
func TestAClockThatRunsBehindDoesNotKeepABrandFromChanging(t *testing.T) {
	product := registered(t)

	early, err := product.ChangeBrand(developers, alice, product.CreatedAt.Add(-time.Hour))
	if err != nil {
		t.Fatalf("change the brand: %v", err)
	}
	if !early.UpdatedAt.Equal(product.CreatedAt) || early.Version != 2 {
		t.Errorf("changed at %s and version %d, want at %s and version 2", early.UpdatedAt, early.Version, product.CreatedAt)
	}

	changed, err := product.ChangeBrand(developers, alice, created.Add(time.Hour))
	if err != nil {
		t.Fatalf("change the brand: %v", err)
	}
	again, err := changed.ChangeBrand(Brand{Name: "CargoPai"}, bob, changed.UpdatedAt.Add(-time.Minute))
	if err != nil {
		t.Fatalf("change the brand again: %v", err)
	}
	if !again.UpdatedAt.Equal(changed.UpdatedAt) || again.Version != 3 {
		t.Errorf("changed again at %s and version %d, want at %s and version 3", again.UpdatedAt, again.Version, changed.UpdatedAt)
	}
}

// The errors are written for a log and for a caller. They repeat nothing of
// the value they refuse.
func TestAnErrorOfABrandRepeatsNothingOfTheValue(t *testing.T) {
	product := registered(t)

	for what, brand := range map[string]Brand{
		"a name":   {Name: " canary name "},
		"a logo":   {Name: "CargoPai", LogoURL: "http://canary.example/logo.svg"},
		"a colour": {Name: "CargoPai", PrimaryColor: "canary"},
	} {
		if _, err := product.ChangeBrand(brand, alice, created); err == nil || strings.Contains(err.Error(), "canary") {
			t.Errorf("%s: the error is %v", what, err)
		}
	}
}
