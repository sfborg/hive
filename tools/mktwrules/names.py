"""Name-element format rules: NomenclaturalRank#validate_name_format,
Protonym#name_is_latinized and Protonym#sv_validate_name."""
from lib import *
from twdata import *

FIELD_WHAT = {
    "col__uninomial": "name",
    "col__infrageneric_epithet": "infrageneric epithet",
    "col__specific_epithet": "specific epithet",
    "col__infraspecific_epithet": "infraspecific epithet",
    "col__scientific_name": "species name",
}

NR = "NomenclaturalRank::"
NRF = "app/models/nomenclatural_rank/"


def _row(code, rank, field, value, status=""):
    cid = CODES[code]["id"] if code else "ZOOLOGICAL"
    v = {"col__code_id": cid, "col__rank_id": rank, field: value}
    if status:
        v["col__status_id"] = status
    if field == "col__infrageneric_epithet":
        v["col__genus"] = "Aus"
        sci = f"Aus ({value})"
    elif field == "col__specific_epithet":
        v["col__genus"] = "Aus"
        sci = f"Aus {value}"
    elif field == "col__infraspecific_epithet":
        v["col__genus"] = "Aus"
        v["col__specific_epithet"] = "bus"
        sci = f"Aus bus {value}"
    else:
        sci = value
    return [name(uid("n"), sci, **v)]


# Family-group names that are synonyms are exempt from the format
# checks (Protonym#has_latinized_exceptions?).
NOT_SYNONYM = cond("col__name_id", "not_exists", None, "name:synonym-usage")
FAMILY_ALL = sorted({r for c in CODES for r in group(c, "family")})


def _synonymised(rows):
    nid = rows[0]["values"]["col__id"]
    return rows + [name(nid + "-valid", "Validus", col__code_id=rows[0]["values"]["col__code_id"]),
                   taxon(nid + "-t", nid + "-valid"), syn(nid + "-t", nid)]


def element(code, rid, rname, field, ranks, vtype, params, msg, tw, bad, good,
            extra=None, fixture_rank=None, enforcement="hard", severity="error", priority=20):
    fam = set(group(code, "family")) if code else set(FAMILY_ALL)
    parts = []
    other = [r for r in ranks if r not in fam]
    family = [r for r in ranks if r in fam]
    if other:
        parts.append((rid, rname, other, False))
    if family:
        parts.append((rid if not other else rid + "_family_group",
                      rname if not other else rname + " (family group)", family, True))
    for prid, prname, pranks, exempt in parts:
        conds = []
        if code:
            conds.append(code_is(code))
        conds += [rank_in(pranks), nonempty(field)]
        conds += extra or []
        if exempt:
            conds.append(NOT_SYNONYM)
        r = rule(prid, "name", field, prname, vtype, enforcement, severity, conds, params, msg, priority, tw)
        fr = fixture_rank if fixture_rank in pranks else pranks[0]
        goods = [(f"{field}={good!r}", _row(code, fr, field, good))]
        if exempt:
            goods.append(("family-group synonym", _synonymised(_row(code, fr, field, bad))))
        add(code, r, [(f"{field}={bad!r}", _row(code, fr, field, bad))], goods)


def regex(pattern):
    return {"pattern": pattern}


CAP = "^[^a-z][^A-Z]*$"
CAP_OR_SHORT = "^(?:.|[^a-z][^A-Z]*)$"
LOWER = "^[^A-Z]*$"


def ending(endings):
    # A one-letter name skips the ending check (TaxonWorks exits early
    # when the name is shorter than two letters).
    return "^(?:.|.*(?:" + "|".join(endings) + "))$"


def not_viral(code, rid, rname, field, ranks, suffix, msg, tw, bad, good):
    """Name must not end in a viral suffix: fires via absence when the
    field matches the suffix."""
    conds = [code_is(code), rank_in(ranks), cond(field, "matches", suffix + "$")]
    r = rule(rid, "name", field, rname, "absence", "hard", "error", conds, {}, msg, 20, tw)
    add(code, r,
        [(f"{field}={bad!r}", _row(code, ranks[0], field, bad))],
        [(f"{field}={good!r}", _row(code, ranks[0], field, good))])


def exc():
    return [cond("col__status_id", "not_in", excepted_form())]


def iczn():
    c = "iczn"
    hfg = group(c, "higher", "family", "genus")
    gg = group(c, "genus")
    x = exc()
    element(c, "tw_iczn_uninomial_capitalized", "Uninomial is capitalized", "col__uninomial", hfg,
            "regex", regex(CAP),
            "The name must start with an uppercase letter followed by lowercase letters.",
            f"{NR}Iczn::HigherClassificationGroup/FamilyGroup/GenusGroup.validate_name_format ({NRF}iczn/*_group.rb)",
            "carabidae", "Carabidae", x, fixture_rank="FAMILY")
    element(c, "tw_iczn_infrageneric_capitalized", "Infrageneric epithet is capitalized",
            "col__infrageneric_epithet", gg, "regex", regex(CAP),
            "The infrageneric epithet must start with an uppercase letter followed by lowercase letters.",
            f"{NR}Iczn::GenusGroup.validate_name_format ({NRF}iczn/genus_group.rb)",
            "bus", "Bus", x, fixture_rank="SUBGENUS")
    element(c, "tw_iczn_uninomial_min_length", "Uninomial has at least two letters", "col__uninomial", hfg,
            "length", {"min": 2}, "The name must be at least two letters long.",
            f"{NR}Iczn::HigherClassificationGroup/FamilyGroup/GenusGroup.validate_name_format ({NRF}iczn/*_group.rb)",
            "A", "Ab", x, fixture_rank="GENUS")
    element(c, "tw_iczn_infrageneric_min_length", "Infrageneric epithet has at least two letters",
            "col__infrageneric_epithet", gg, "length", {"min": 2},
            "The infrageneric epithet must be at least two letters long.",
            f"{NR}Iczn::GenusGroup.validate_name_format ({NRF}iczn/genus_group.rb)",
            "B", "Bb", x, fixture_rank="SUBGENUS")
    element(c, "tw_iczn_kingdom_name", "Kingdom name is Animalia or Protozoa", "col__uninomial", ["KINGDOM"],
            "regex", regex("^(?:.|Animalia|Protozoa)$"),
            "Under the ICZN the kingdom name must be Animalia or Protozoa.",
            f"{NR}Iczn::HigherClassificationGroup::Kingdom.validate_name_format ({NRF}iczn/higher_classification_group/kingdom.rb)",
            "Plantae", "Animalia", x)
    for rid, rname, ranks, suf, cls, f in [
        ("tw_iczn_superfamily_ending", "Superfamily name ends in -oidea", ["SUPERFAMILY"], "oidea", "Superfamily", "superfamily"),
        ("tw_iczn_family_ending", "Family name ends in -idae", ["FAMILY"], "idae", "Family", "family"),
        ("tw_iczn_subfamily_ending", "Subfamily name ends in -inae", ["SUBFAMILY", "SUPERSUBFAMILY"], "inae", "Subfamily", "subfamily"),
        ("tw_iczn_tribe_ending", "Tribe name ends in -ini", ["TRIBE"], "ini", "Tribe", "tribe"),
        ("tw_iczn_subtribe_ending", "Subtribe name ends in -ina", ["SUBTRIBE"], "ina", "Subtribe", "subtribe"),
    ]:
        element(c, rid, rname, "col__uninomial", ranks, "regex", regex(ending([suf])),
                f"A {ranks[0].lower()} name must end in -{suf}.",
                f"{NR}Iczn::FamilyGroup::{cls}.validate_name_format ({NRF}iczn/family_group/{f}.rb)",
                "Carabus", "Carab" + suf, x)
    sp = SPECIES_TERMINAL[c]
    isp = INFRASPECIES_TERMINAL[c]
    for field, ranks, fr, what in [("col__specific_epithet", sp, "SPECIES", "specific"),
                                   ("col__infraspecific_epithet", isp, "SUBSPECIES", "infraspecific")]:
        element(c, f"tw_iczn_{what}_epithet_lowercase", f"{what.capitalize()} epithet is lowercase", field, ranks,
                "regex", regex(LOWER), f"The {what} epithet must be lowercase.",
                f"{NR}Iczn::SpeciesGroup.validate_name_format ({NRF}iczn/species_group.rb)",
                "Bus", "bus", x, fixture_rank=fr)
        element(c, f"tw_iczn_{what}_epithet_min_length", f"{what.capitalize()} epithet has at least two letters",
                field, ranks, "length", {"min": 2}, f"The {what} epithet must be at least two letters long.",
                f"{NR}Iczn::SpeciesGroup.validate_name_format ({NRF}iczn/species_group.rb)",
                "b", "bu", x, fixture_rank=fr)


FAMILY_ICN = ["aceae", "Compositae", "Cruciferae", "Gramineae", "Guttiferae", "Labiatae",
              "Leguminosae", "Palmae", "Umbelliferae"]


def icn():
    c = "icn"
    x = exc()
    hfg = group(c, "higher", "family", "genus")
    hg = group(c, "higher", "genus")
    gg = group(c, "genus")
    t = f"{NR}Icn::HigherClassificationGroup/FamilyGroup/GenusGroup.validate_name_format ({NRF}icn/*_group.rb)"
    element(c, "tw_icn_uninomial_capitalized", "Uninomial is capitalized", "col__uninomial", hfg, "regex",
            regex(CAP), "The name must start with an uppercase letter followed by lowercase letters.", t,
            "rosaceae", "Rosaceae", x, fixture_rank="FAMILY")
    element(c, "tw_icn_infrageneric_capitalized", "Infrageneric epithet is capitalized",
            "col__infrageneric_epithet", gg, "regex", regex(CAP),
            "The infrageneric epithet must start with an uppercase letter followed by lowercase letters.",
            f"{NR}Icn::GenusGroup.validate_name_format ({NRF}icn/genus_group.rb)",
            "bus", "Bus", x, fixture_rank="SUBGENUS")
    element(c, "tw_icn_uninomial_min_length", "Uninomial has at least two letters", "col__uninomial", hg,
            "length", {"min": 2}, "The name must be at least two letters long.", t, "A", "Ab", x,
            fixture_rank="GENUS")
    element(c, "tw_icn_infrageneric_min_length", "Infrageneric epithet has at least two letters",
            "col__infrageneric_epithet", gg, "length", {"min": 2},
            "The infrageneric epithet must be at least two letters long.",
            f"{NR}Icn::GenusGroup.validate_name_format ({NRF}icn/genus_group.rb)", "B", "Bb", x,
            fixture_rank="SUBGENUS")
    for field, ranks, fr, what in [("col__specific_epithet", SPECIES_TERMINAL[c], "SPECIES", "specific"),
                                   ("col__infraspecific_epithet", INFRASPECIES_TERMINAL[c], "VARIETY", "infraspecific")]:
        element(c, f"tw_icn_{what}_epithet_min_length", f"{what.capitalize()} epithet has at least two letters",
                field, ranks, "length", {"min": 2}, f"The {what} epithet must be at least two letters long.",
                f"{NR}Icn::SpeciesAndInfraspeciesGroup.validate_name_format ({NRF}icn/species_and_infraspecies_group.rb)",
                "b", "bu", x, fixture_rank=fr)
    element(c, "tw_icn_kingdom_name", "Kingdom name is Plantae, Chromista or Fungi", "col__uninomial", ["KINGDOM"],
            "regex", regex("^(?:.|Plantae|Chromista|Fungi)$"),
            "Under the ICN the kingdom name must be Plantae, Chromista or Fungi.",
            f"{NR}Icn::HigherClassificationGroup::Kingdom.validate_name_format ({NRF}icn/higher_classification_group/kingdom.rb)",
            "Animalia", "Plantae", x)
    hcg = f"{NR}Icn::HigherClassificationGroup"
    fgp = f"{NR}Icn::FamilyGroup"
    for rid, rname, rank, ends, shown, cls, f, bad, good in [
        ("tw_icn_phylum_ending", "Phylum name ends in -phyta, -phycota or -mycota", "PHYLUM",
         ["phyta", "phycota", "mycota"], "-phyta, -phycota or -mycota", hcg + "::Phylum", "higher_classification_group/phylum", "Rosales", "Magnoliophyta"),
        ("tw_icn_subphylum_ending", "Subphylum name ends in -phytina, -phycotina or -mycotina", "SUBPHYLUM",
         ["phytina", "phycotina", "mycotina"], "-phytina, -phycotina or -mycotina", hcg + "::Subphylum", "higher_classification_group/subphylum", "Rosales", "Pezizomycotina"),
        ("tw_icn_class_ending", "Class name ends in -opsida, -phyceae or -mycetes", "CLASS",
         ["opsida", "phyceae", "mycetes"], "-opsida, -phyceae or -mycetes", hcg + "::ClassRank", "higher_classification_group/class_rank", "Rosales", "Magnoliopsida"),
        ("tw_icn_subclass_ending", "Subclass name ends in -idae", "SUBCLASS",
         ["idae"], "-idae, -phycidae or -mycetidae", hcg + "::Subclass", "higher_classification_group/subclass", "Rosales", "Rosidae"),
        ("tw_icn_superorder_ending", "Superorder name ends in -anae", "SUPERORDER",
         ["anae"], "-anae", hcg + "::Superorder", "higher_classification_group/superorder", "Rosales", "Rosanae"),
        ("tw_icn_order_ending", "Order name ends in -ales", "ORDER",
         ["ales"], "-ales", hcg + "::Order", "higher_classification_group/order", "Rosaceae", "Rosales"),
        ("tw_icn_suborder_ending", "Suborder name ends in -ineae", "SUBORDER",
         ["ineae"], "-ineae", hcg + "::Suborder", "higher_classification_group/suborder", "Rosales", "Rosineae"),
        ("tw_icn_family_ending", "Family name ends in -aceae", "FAMILY",
         FAMILY_ICN, "-aceae (or is one of the conserved alternative family names)", fgp + "::Family", "family_group/family", "Rosales", "Rosaceae"),
        ("tw_icn_subfamily_ending", "Subfamily name ends in -oideae", "SUBFAMILY",
         ["oideae"], "-oideae", fgp + "::Subfamily", "family_group/subfamily", "Rosaceae", "Rosoideae"),
        ("tw_icn_tribe_ending", "Tribe name ends in -eae", "TRIBE",
         ["eae"], "-eae", fgp + "::Tribe", "family_group/tribe", "Rosales", "Roseae"),
        ("tw_icn_subtribe_ending", "Subtribe name ends in -inae", "SUBTRIBE",
         ["inae"], "-inae", fgp + "::Subtribe", "family_group/subtribe", "Rosales", "Rosinae"),
    ]:
        pat = "^(?:.|.*(?:" + "|".join(e for e in ends if e[0].islower()) + ")" + \
              "".join("|" + e for e in ends if e[0].isupper()) + ")$"
        element(c, rid, rname, "col__uninomial", [rank], "regex", regex(pat),
                f"A {rank.lower()} name must end in {shown}.",
                f"{cls}.validate_name_format ({NRF}icn/{f}.rb)", bad, good, x)
    for rid, rank, suf, cls, f in [
        ("tw_icn_subclass_not_viral", "SUBCLASS", "viridae", hcg + "::Subclass", "higher_classification_group/subclass"),
        ("tw_icn_superorder_not_viral", "SUPERORDER", "viranae", hcg + "::Superorder", "higher_classification_group/superorder"),
        ("tw_icn_order_not_viral", "ORDER", "virales", hcg + "::Order", "higher_classification_group/order"),
        ("tw_icn_suborder_not_viral", "SUBORDER", "virineae", hcg + "::Suborder", "higher_classification_group/suborder"),
        ("tw_icn_tribe_not_viral", "TRIBE", "vireae", fgp + "::Tribe", "family_group/tribe"),
        ("tw_icn_subtribe_not_viral", "SUBTRIBE", "virinae", fgp + "::Subtribe", "family_group/subtribe"),
    ]:
        good = {"viridae": "Rosidae", "viranae": "Rosanae", "virales": "Rosales", "virineae": "Rosineae",
                "vireae": "Roseae", "virinae": "Rosinae"}[suf]
        not_viral(c, rid, f"{rank.capitalize()} name does not end in -{suf}", "col__uninomial", [rank], suf,
                  f"A {rank.lower()} name under the ICN must not end in -{suf}, which is a virus-name ending.",
                  f"{cls}.validate_name_format ({NRF}icn/{f}.rb)", "Ro" + suf, good)
    for field, rid in [("col__uninomial", "tw_icn_genus_group_not_viral"),
                       ("col__infrageneric_epithet", "tw_icn_infrageneric_not_viral")]:
        not_viral(c, rid, "Genus-group name does not end in -virus", field, gg, "virus",
                  "A genus-group name under the ICN must not end in -virus.",
                  f"{NR}Icn::GenusGroup.validate_name_format ({NRF}icn/genus_group.rb)", "Rosavirus", "Rosa")


def icnp():
    c = "icnp"
    x = exc()
    hfg = group(c, "higher", "family", "genus")
    hg = group(c, "higher", "genus")
    gg = group(c, "genus")
    t = f"{NR}Icnp::HigherClassificationGroup/FamilyGroup/GenusGroup.validate_name_format ({NRF}icnp/*_group.rb)"
    element(c, "tw_icnp_uninomial_capitalized", "Uninomial is capitalized", "col__uninomial", hfg, "regex",
            regex(CAP), "The name must start with an uppercase letter followed by lowercase letters.", t,
            "bacillaceae", "Bacillaceae", x, fixture_rank="FAMILY")
    element(c, "tw_icnp_infrageneric_capitalized", "Infrageneric epithet is capitalized",
            "col__infrageneric_epithet", gg, "regex", regex(CAP),
            "The infrageneric epithet must start with an uppercase letter followed by lowercase letters.",
            f"{NR}Icnp::GenusGroup.validate_name_format ({NRF}icnp/genus_group.rb)",
            "bus", "Bus", x, fixture_rank="SUBGENUS")
    element(c, "tw_icnp_uninomial_min_length", "Uninomial has at least two letters", "col__uninomial", hg,
            "length", {"min": 2}, "The name must be at least two letters long.", t, "A", "Ab", x,
            fixture_rank="GENUS")
    element(c, "tw_icnp_infrageneric_min_length", "Infrageneric epithet has at least two letters",
            "col__infrageneric_epithet", gg, "length", {"min": 2},
            "The infrageneric epithet must be at least two letters long.",
            f"{NR}Icnp::GenusGroup.validate_name_format ({NRF}icnp/genus_group.rb)", "B", "Bb", x,
            fixture_rank="SUBGENUS")
    for field, ranks, fr, what in [("col__specific_epithet", SPECIES_TERMINAL[c], "SPECIES", "specific"),
                                   ("col__infraspecific_epithet", INFRASPECIES_TERMINAL[c], "SUBSPECIES", "infraspecific")]:
        t2 = f"{NR}Icnp::SpeciesGroup.validate_name_format ({NRF}icnp/species_group.rb)"
        element(c, f"tw_icnp_{what}_epithet_lowercase", f"{what.capitalize()} epithet is lowercase", field, ranks,
                "regex", regex(LOWER), f"The {what} epithet must be lowercase.", t2, "Bus", "bus", x, fixture_rank=fr)
        element(c, f"tw_icnp_{what}_epithet_min_length", f"{what.capitalize()} epithet has at least two letters",
                field, ranks, "length", {"min": 2}, f"The {what} epithet must be at least two letters long.",
                t2, "b", "bu", x, fixture_rank=fr)
    element(c, "tw_icnp_kingdom_name", "Kingdom name is Archaea or Bacteria", "col__uninomial", ["KINGDOM"],
            "regex", regex("^(?:.|Archaea|Bacteria)$"),
            "Under the ICNP the kingdom name must be Archaea or Bacteria.",
            f"{NR}Icnp::HigherClassificationGroup::Kingdom.validate_name_format ({NRF}icnp/higher_classification_group/kingdom.rb)",
            "Plantae", "Bacteria", x)
    hcg = f"{NR}Icnp::HigherClassificationGroup"
    fgp = f"{NR}Icnp::FamilyGroup"
    for rid, rname, rank, ends, shown, cls, f, bad, good in [
        ("tw_icnp_order_ending", "Order name ends in -ales", "ORDER", ["ales"], "-ales",
         hcg + "::Order", "higher_classification_group/order", "Bacillaceae", "Bacillales"),
        ("tw_icnp_suborder_ending", "Suborder name ends in -ineae", "SUBORDER", ["ineae"], "-ineae",
         hcg + "::Suborder", "higher_classification_group/suborder", "Bacillales", "Bacillineae"),
        ("tw_icnp_family_ending", "Family name ends in -aceae", "FAMILY", FAMILY_ICN,
         "-aceae", fgp + "::Family", "family_group/family", "Bacillales", "Bacillaceae"),
        ("tw_icnp_subfamily_ending", "Subfamily name ends in -oideae", "SUBFAMILY", ["oideae"], "-oideae",
         fgp + "::Subfamily", "family_group/subfamily", "Bacillaceae", "Bacilloideae"),
        ("tw_icnp_tribe_ending", "Tribe name ends in -eae", "TRIBE", ["eae"], "-eae",
         fgp + "::Tribe", "family_group/tribe", "Bacillales", "Bacilleae"),
        ("tw_icnp_subtribe_ending", "Subtribe name ends in -inae", "SUBTRIBE", ["inae"], "-inae",
         fgp + "::Subtribe", "family_group/subtribe", "Bacillales", "Bacillinae"),
    ]:
        pat = "^(?:.|.*(?:" + "|".join(e for e in ends if e[0].islower()) + ")" + \
              "".join("|" + e for e in ends if e[0].isupper()) + ")$"
        element(c, rid, rname, "col__uninomial", [rank], "regex", regex(pat),
                f"A {rank.lower()} name must end in {shown}.",
                f"{cls}.validate_name_format ({NRF}icnp/{f}.rb)", bad, good, x)


ICVCN_CLS = {"realm": "Realm", "kingdom": "Kingdom", "phylum": "Phylum", "class_rank": "ClassRank",
             "order": "Order", "family": "Family", "subfamily": "Subfamily", "genus": "Genus"}


def icvcn():
    c = "icvcn"
    cap_ranks = ["PHYLUM", "CLASS", "ORDER", "FAMILY", "SUBFAMILY", "GENUS"]
    element(c, "tw_icvcn_uninomial_capitalized", "Uninomial is capitalized", "col__uninomial", cap_ranks,
            "regex", regex(CAP_OR_SHORT), "The name must start with an uppercase letter followed by lowercase letters.",
            f"{NR}Icvcn::Phylum/ClassRank/Order/Family/Subfamily/Genus.validate_name_format ({NRF}icvcn/*.rb)", "tobamovirus", "Tobamovirus",
            fixture_rank="GENUS")
    for rid, rname, rank, ends, shown, f, bad, good in [
        ("tw_icvcn_realm_ending", "Realm name ends in -viria", "REALM", ["viria"], "-viria", "realm", "Riboviridae", "Riboviria"),
        ("tw_icvcn_kingdom_ending", "Kingdom name ends in -virae", "KINGDOM", ["virae"], "-virae", "kingdom", "Riboviria", "Orthornavirae"),
        ("tw_icvcn_phylum_ending", "Phylum name ends in -viricota", "PHYLUM", ["viricota"], "-viricota", "phylum", "Pisuviridae", "Pisuviricota"),
        ("tw_icvcn_class_ending", "Class name ends in -viricetes", "CLASS", ["viricetes"], "-viricetes", "class_rank", "Pisuviricota", "Pisoniviricetes"),
        ("tw_icvcn_order_ending", "Order name ends in -virales", "ORDER", ["virales"], "-virales", "order", "Pisoniviricetes", "Nidovirales"),
        ("tw_icvcn_family_ending", "Family name ends in -viridae, -viroidae or -satellitidae", "FAMILY",
         ["viridae", "viroidae", "satellitidae"], "-viridae, -viroidae or -satellitidae", "family", "Nidovirales", "Coronaviridae"),
        ("tw_icvcn_subfamily_ending", "Subfamily name ends in -virinae, -viroinae or -satellitinae", "SUBFAMILY",
         ["virinae", "viroinae", "satellitinae"], "-virinae, -viroinae or -satellitinae", "subfamily", "Coronaviridae", "Orthocoronavirinae"),
        ("tw_icvcn_genus_ending", "Genus name ends in -virus, -viroid or -satellite", "GENUS",
         ["virus", "viroid", "satellite"], "-virus, -viroid or -satellite", "genus", "Coronaviridae", "Betacoronavirus"),
    ]:
        element(c, rid, rname, "col__uninomial", [rank], "regex", regex(ending(ends)),
                f"A {rank.lower()} name must end in {shown}.",
                f"{NR}Icvcn::{ICVCN_CLS[f]}.validate_name_format ({NRF}icvcn/{f}.rb)",
                bad, good)
    t = f"{NR}Icvcn::Species.validate_name_format ({NRF}icvcn/species.rb)"
    element(c, "tw_icvcn_species_capitalized", "Species name is capitalized", "col__scientific_name", ["SPECIES"],
            "regex", regex(CAP_OR_SHORT),
            "The species name must start with an uppercase letter; the remaining letters must be lowercase.",
            t, "tobacco mosaic virus", "Tobacco mosaic virus")
    element(c, "tw_icvcn_species_ending", "Species name ends in virus, viroid or satellite", "col__scientific_name",
            ["SPECIES"], "regex", regex("^(?:.|.* (?:virus|viroid|satellite))$"),
            "The last word of a virus species name must be virus, viroid or satellite.",
            t, "Tobamovirus tabaci", "Tobacco mosaic virus")


def latinized_rules():
    t = "Protonym#name_is_latinized (app/models/protonym.rb)"
    x = [cond("col__status_id", "not_in", excepted_form())]
    units = [("col__uninomial", "GENUS", "tw_uninomial_latinized", [cond("col__rank_id", "not_in", FAMILY_ALL)]),
             ("col__uninomial", "FAMILY", "tw_family_group_uninomial_latinized", [rank_in(FAMILY_ALL), NOT_SYNONYM]),
             ("col__infrageneric_epithet", "SUBGENUS", "tw_infrageneric_epithet_latinized", []),
             ("col__specific_epithet", "SPECIES", "tw_specific_epithet_latinized", []),
             ("col__infraspecific_epithet", "SUBSPECIES", "tw_infraspecific_epithet_latinized", [])]
    for field, fr, rid, scope in units:
        what = FIELD_WHAT[field]
        conds = [nonempty(field)] + scope + x
        if field in ("col__specific_epithet", "col__infraspecific_epithet"):
            # ICVCN species names are exempt; they may contain spaces.
            conds.append(cond("col__code_id", "not_equals", "VIRUS"))
        rname = f"{what.capitalize()} is latinized" + (" (family group)" if fr == "FAMILY" else "")
        r = rule(rid, "name", field, rname, "regex", "hard", "error", conds, regex("^[a-zA-Z|-]*$"),
                 f"The {what} must be latinized: letters only, with no digits, spaces or diacritics.", 20, t)
        bad = {"col__uninomial": "Müllerella", "col__infrageneric_epithet": "Bus2",
               "col__specific_epithet": "b-us 2", "col__infraspecific_epithet": "bús"}[field]
        good = {"col__uninomial": "Muellerella", "col__infrageneric_epithet": "Bus",
                "col__specific_epithet": "c-album", "col__infraspecific_epithet": "bus"}[field]
        goods = [(f"{field}={good!r}", _row(None, fr, field, good)),
                 ("excepted status", _row(None, fr, field, bad, excepted_form()[0]))]
        if fr == "FAMILY":
            goods.append(("family-group synonym", _synonymised(_row(None, fr, field, bad))))
        add(None, r, [(f"{field}={bad!r}", _row(None, fr, field, bad))], goods)


def validate_name():
    """Protonym#sv_validate_name: stricter character and hyphen rules,
    skipped for unavailable names."""
    t = "Protonym#sv_validate_name (app/models/protonym/soft_validation_extensions.rb)"
    pats = {"iczn": "^(?:[a-zA-Z]*|[a-zA-Z]-[a-zA-Z]*)$", "icnp": "^(?:[a-zA-Z]*|[a-zA-Z]-[a-zA-Z]*)$",
            "icn": "^[a-zA-Z]*(?:-[a-zA-Z]*)?$"}
    for c in ("iczn", "icn", "icnp"):
        for field, ranks, fr in [
            ("col__uninomial", group(c, "higher", "family", "genus"), "GENUS"),
            ("col__infrageneric_epithet", group(c, "genus"), "SUBGENUS"),
            ("col__specific_epithet", SPECIES_TERMINAL[c], "SPECIES"),
            ("col__infraspecific_epithet", INFRASPECIES_TERMINAL[c], "SUBSPECIES" if c != "icn" else "VARIETY"),
        ]:
            what = FIELD_WHAT[field]
            conds = [code_is(c), rank_in(ranks), nonempty(field),
                     cond("col__status_id", "not_in", unavailable(c))]
            hint = ("a single letter, a hyphen, then letters (for example c-album)"
                    if c != "icn" else "two parts joined by one hyphen")
            r = rule(f"tw_{c}_{field[5:]}_format", "name", field, f"{what.capitalize()} has no special characters",
                     "regex", "soft", "warn", conds, regex(pats[c]),
                     f"The {what} should contain only letters; a hyphen is allowed only as {hint}. "
                     "Mark misspellings with the appropriate status instead.", 30, t)
            bad = "ab-cd-ef"
            good = "c-album" if c != "icn" else "uva-ursi"
            if field in ("col__uninomial", "col__infrageneric_epithet"):
                bad, good = "Ab-cd-ef", "Abcd"
            add(c, r, [(f"{field}={bad!r}", _row(c, fr, field, bad))],
                [(f"{field}={good!r}", _row(c, fr, field, good))])


def build():
    iczn()
    icn()
    icnp()
    icvcn()
    latinized_rules()
    validate_name()
