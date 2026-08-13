package orcid

import "context"

// Person is a resolved ORCID record. All string fields are best-effort:
// public records may hide any of them, in which case they arrive
// empty.
type Person struct {
	// ORCID is the normalized 19-char form (four dash-separated groups
	// of four, e.g. "0000-0002-1825-0097").
	ORCID string
	// GivenNames — the "given-names" field on the ORCID record.
	GivenNames string
	// FamilyName — the "family-name" field on the ORCID record.
	FamilyName string
	// CreditName — the researcher's preferred citation form when they
	// have set one. Empty when unset; callers should fall back to
	// "GivenNames FamilyName".
	CreditName string
}

// DisplayName returns the credit name when set, else "given family",
// else whichever half is present, else the raw ORCID iD.
func (p Person) DisplayName() string {
	if p.CreditName != "" {
		return p.CreditName
	}
	switch {
	case p.GivenNames != "" && p.FamilyName != "":
		return p.GivenNames + " " + p.FamilyName
	case p.FamilyName != "":
		return p.FamilyName
	case p.GivenNames != "":
		return p.GivenNames
	}
	return p.ORCID
}

// Lookup fetches the personal-details record for orcid. Accepts the
// canonical dashed form or an orcid.org URL; extra whitespace is
// trimmed. Returns [*InvalidIDError] (matching [ErrInvalidID]) for
// syntactic problems and [*NotFoundError] (matching [ErrNotFound])
// when the iD is well-formed but unknown to ORCID.
func (c *Client) Lookup(ctx context.Context, orcid string) (*Person, error) {
	norm, err := Normalize(orcid)
	if err != nil {
		return nil, err
	}
	var raw personalDetails
	if err := c.get(ctx, "/"+norm+"/personal-details", nil, &raw); err != nil {
		// Attach the input iD to NotFoundError for better error text.
		if nfe, ok := err.(*NotFoundError); ok {
			nfe.ID = norm
		}
		return nil, err
	}
	return &Person{
		ORCID:      norm,
		GivenNames: raw.Name.GivenNames.value(),
		FamilyName: raw.Name.FamilyName.value(),
		CreditName: raw.Name.CreditName.value(),
	}, nil
}

// personalDetails matches GET /v3.0/{orcid}/personal-details.
// Only the fields hive maps are decoded; extra keys are ignored.
type personalDetails struct {
	Name orcidName `json:"name"`
}

type orcidName struct {
	GivenNames orcidValue `json:"given-names"`
	FamilyName orcidValue `json:"family-name"`
	CreditName orcidValue `json:"credit-name"`
}

// orcidValue wraps ORCID's `{"value": "..."}` envelope. The wrapper
// is nullable — an unset field arrives as JSON null, which decodes
// to a zero-valued struct here.
type orcidValue struct {
	Value *string `json:"value"`
}

func (v orcidValue) value() string {
	if v.Value == nil {
		return ""
	}
	return *v.Value
}
