"""TaxonWorks-derived vocabularies used by the rule generator.

tw_classifications.jsonl holds one line per TaxonNameClassification
class: its NOMEN URI, code, applicable-rank constant, year window,
constant memberships and whether it defines its own
sv_not_specific_classes message. The values were resolved by loading
the TaxonWorks class files (TaxonWorks commit 70f884140a). Rank lists
are the TaxonWorks rank order per code, expressed as SFGA rank ids.
"""
import json
import os

HERE = os.path.dirname(os.path.abspath(__file__))

CLS = [json.loads(l) for l in open(os.path.join(HERE, "tw_classifications.jsonl"))]
CLS_BY = {c["class"]: c for c in CLS}
P = "TaxonNameClassification::"

NOMEN = "http://purl.obolibrary.org/obo/"

CODES = {
    "iczn": {"id": "ZOOLOGICAL", "label": "ICZN", "tw": "Iczn"},
    "icn": {"id": "BOTANICAL", "label": "ICN", "tw": "Icn"},
    "icnp": {"id": "BACTERIAL", "label": "ICNP", "tw": "Icnp"},
    "icvcn": {"id": "VIRUS", "label": "ICVCN", "tw": "Icvcn"},
}

# TaxonWorks rank order (NomenclaturalRank.ordered_ranks) per code, as
# SFGA rank ids, split into TaxonWorks rank groups.
RANKS = {
    "iczn": {
        "higher": ["SUPERKINGDOM", "KINGDOM", "SUBKINGDOM", "INFRAKINGDOM", "SUPERPHYLUM", "PHYLUM",
                   "SUBPHYLUM", "INFRAPHYLUM", "SUPERCLASS", "CLASS", "SUBCLASS", "INFRACLASS",
                   "SUPERCOHORT", "COHORT", "MAGNORDER", "SUPERORDER", "MIRORDER", "ORDER", "SUBORDER",
                   "INFRAORDER", "PARVORDER", "NANORDER"],
        "family": ["SUPERFAMILY", "EPIFAMILY", "FAMILY", "SUPERSUBFAMILY", "SUBFAMILY", "INFRAFAMILY",
                   "SUPERTRIBE", "TRIBE", "SUBTRIBE", "INFRATRIBE"],
        "genus": ["SUPERGENUS", "GENUS", "SUPERSUPERSUBGENUS", "SUPERSUBGENUS", "SUBGENUS", "INFRAGENUS"],
        "species": ["SUPERSUPERSPECIES", "SUPERSPECIES", "SUBSUPERSPECIES", "SPECIES", "SUBSPECIES"],
    },
    "icn": {
        "higher": ["KINGDOM", "SUBKINGDOM", "PHYLUM", "SUBPHYLUM", "CLASS", "SUBCLASS", "SUPERORDER",
                   "ORDER", "SUBORDER"],
        "family": ["FAMILY", "SUBFAMILY", "TRIBE", "SUBTRIBE"],
        "genus": ["GENUS", "SUBGENUS", "SECTION", "SUBSECTION", "SERIES", "SUBSERIES"],
        "species": ["SPECIES", "SUBSPECIES", "VARIETY", "SUBVARIETY", "FORM", "SUBFORM"],
    },
    "icnp": {
        "higher": ["KINGDOM", "PHYLUM", "CLASS", "SUBCLASS", "ORDER", "SUBORDER"],
        "family": ["FAMILY", "SUBFAMILY", "TRIBE", "SUBTRIBE"],
        "genus": ["GENUS", "SUBGENUS"],
        "species": ["SPECIES", "SUBSPECIES"],
    },
    # ICVCN Realm, Phylum and Class belong to no TaxonWorks rank group.
    "icvcn": {
        "order": ["REALM", "KINGDOM", "PHYLUM", "CLASS", "ORDER", "FAMILY", "SUBFAMILY", "GENUS", "SPECIES"],
        "higher": ["KINGDOM", "ORDER"],
        "family": ["FAMILY", "SUBFAMILY"],
        "genus": ["GENUS"],
        "species": ["SPECIES"],
    },
}

# SFGA spells some TaxonWorks ranks differently; rules list both ids.
ALIASES = {
    "SECTION": ["SECTION_BOTANY"],
    "SUBSECTION": ["SUBSECTION_BOTANY"],
    "SUPERSPECIES": ["SPECIES_AGGREGATE"],
}


def expand(ranks):
    out = []
    for r in ranks:
        if r not in out:
            out.append(r)
        for a in ALIASES.get(r, []):
            if a not in out:
                out.append(a)
    return out


def group(code, *names):
    out = []
    for n in names:
        out += RANKS[code][n]
    return expand(out)


def order(code):
    if code == "icvcn":
        return expand(RANKS[code]["order"])
    return group(code, "higher", "family", "genus", "species")


# Genus-group ranks stored as a uninomial vs. as an infrageneric
# epithet, and species-group ranks by terminal epithet field.
GENUS_UNINOMIAL = {"iczn": ["SUPERGENUS", "GENUS"], "icn": ["GENUS"], "icnp": ["GENUS"], "icvcn": ["GENUS"]}
SPECIES_TERMINAL = {
    "iczn": expand(["SUPERSUPERSPECIES", "SUPERSPECIES", "SUBSUPERSPECIES", "SPECIES"]),
    "icn": ["SPECIES"],
    "icnp": ["SPECIES"],
    "icvcn": ["SPECIES"],
}
INFRASPECIES_TERMINAL = {
    "iczn": ["SUBSPECIES"],
    "icn": ["SUBSPECIES", "VARIETY", "SUBVARIETY", "FORM", "SUBFORM"],
    "icnp": ["SUBSPECIES"],
    "icvcn": [],
}

# valid_parents per rank (NomenclaturalRank subclasses). None means
# the group default, which is built from the group lists below.
PARENT_OVERRIDES = {
    "iczn": {
        "SUBKINGDOM": ["KINGDOM"], "INFRAKINGDOM": ["SUBKINGDOM"], "SUBPHYLUM": ["PHYLUM"],
        "INFRAPHYLUM": ["SUBPHYLUM"], "SUBCLASS": ["CLASS"], "INFRACLASS": ["SUBCLASS"],
        "SUBORDER": ["ORDER"], "INFRAORDER": ["SUBORDER"], "MIRORDER": ["SUPERORDER"],
        "SUPERSUBFAMILY": ["FAMILY"], "SUBFAMILY": ["FAMILY", "SUPERSUBFAMILY"],
        "INFRAFAMILY": ["SUBFAMILY"], "SUPERTRIBE": ["SUBFAMILY"], "TRIBE": ["SUPERTRIBE", "SUBFAMILY"],
        "SUBTRIBE": ["TRIBE"], "INFRATRIBE": ["SUBTRIBE"],
        "SUPERSUPERSUBGENUS": ["GENUS"], "SUPERSUBGENUS": ["GENUS"], "SUBGENUS": ["GENUS"],
        "INFRAGENUS": ["SUBGENUS"],
        "SUBSUPERSPECIES": ["SUPERSPECIES"], "SUBSPECIES": ["SPECIES"],
    },
    "icn": {
        "SUBKINGDOM": ["KINGDOM"], "SUBPHYLUM": ["PHYLUM"], "SUBCLASS": ["CLASS"], "SUBORDER": ["ORDER"],
        "SUBFAMILY": ["FAMILY"], "TRIBE": ["SUBFAMILY"], "SUBTRIBE": ["TRIBE"],
        "SUBGENUS": ["GENUS"], "SECTION": ["GENUS", "SUBGENUS"], "SUBSECTION": ["SECTION"],
        "SERIES": ["GENUS", "SUBGENUS", "SECTION", "SUBSECTION"], "SUBSERIES": ["SERIES"],
        "SUBSPECIES": ["SPECIES"], "VARIETY": ["SPECIES", "SUBSPECIES"], "SUBVARIETY": ["VARIETY"],
        "FORM": ["SPECIES", "SUBSPECIES", "VARIETY", "SUBVARIETY"], "SUBFORM": ["FORM"],
    },
    "icnp": {
        "SUBCLASS": ["CLASS"], "SUBORDER": ["ORDER"], "SUBFAMILY": ["FAMILY"], "TRIBE": ["SUBFAMILY"],
        "SUBTRIBE": ["TRIBE"], "SUBGENUS": ["GENUS"], "SUBSPECIES": ["SPECIES"],
    },
}
PARENT_DEFAULTS = {
    "higher": ["higher"],
    "family": ["higher", "family"],
    "genus": ["family", "genus"],
    "species": ["genus", "species"],
}


def classes(code=None, pred=None):
    out = []
    for c in CLS:
        if not c["persistable"] or not c["nomen_uri"]:
            continue
        if code and c["code"] != code:
            continue
        if pred and not pred(c):
            continue
        out.append(c)
    return out


def uris(cs):
    return sorted({c["nomen_uri"] for c in cs})


def member(const):
    return lambda c: const in (c.get("member_of_constants") or [])


def status_uris(code):
    return uris(classes(code))


def unavailable(code):
    return uris(classes(code, member("TAXON_NAME_CLASS_NAMES_UNAVAILABLE")))


def unavailable_or_invalid(code):
    return uris(classes(code, member("TAXON_NAME_CLASS_NAMES_UNAVAILABLE_AND_INVALID")))


def valid(code):
    return uris(classes(code, member("TAXON_NAME_CLASS_NAMES_VALID")))


def excepted_form():
    return uris(classes(None, member("EXCEPTED_FORM_TAXON_NAME_CLASSIFICATIONS")))


def latinized():
    return uris(classes(None, lambda c: c["class"].startswith(P + "Latinized")))


def by_class(*names):
    return uris([CLS_BY[P + n] for n in names])


def descendants_and_self(name):
    full = P + name
    return uris(classes(None, lambda c: c["class"] == full or c["class"].startswith(full + "::")))


CODE_ROOTS = uris([c for c in CLS if c["class"] in (P + "Iczn", P + "Icn", P + "Icnp", P + "Icvcn")])


def label(uri):
    for c in CLS:
        if c["nomen_uri"] == uri:
            return c.get("classification_label") or c["class"]
    return uri
