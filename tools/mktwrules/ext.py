"""Rules that need the TaxonWorks round-trip columns (the tw__ proposal
against the sfga schema, not yet adopted).

Each bundle declares the tw__ tables and columns it needs in its
Requires block; hive skips a bundle the archive cannot satisfy and says
which columns were missing, so a rename upstream surfaces as "these
rules did not run".
"""
from lib import *
from twdata import *

SVE = "app/models/protonym/soft_validation_extensions.rb"
TNC = "app/models/taxon_name_classification.rb"
TNRF = "app/models/taxon_name_relationship"
CLASSIFICATION = "tw__nomenclatural_classification"
NR_TYPE = "tw__name_relationship_type"
OC = "TaxonNameRelationship::OriginalCombination::"
CB = "TaxonNameRelationship::Combination::"
TYPIFICATION = "TaxonNameRelationship::Typification::"


def pat(uris):
    return "(?:" + "|".join(u.replace(".", "\\.") for u in uris) + ")"


def has_status(uris, relation=None):
    return cond(CLASSIFICATION, "matches", pat(uris), relation)


def srow(code, statuses, rank="SPECIES", year="", **kw):
    v = {"col__code_id": CODES[code]["id"], "col__rank_id": rank,
         CLASSIFICATION: "|".join(statuses)}
    if year:
        v["col__published_in_year"] = year
    v.update(kw)
    return [name(uid("n"), "Aus bus", **v)]


# ---------------------------------------------------------------- status

def status_rules():
    """The status rules restated against the pipe-separated classification
    list, which is what TaxonWorks validates: a name carries any number
    of classifications, and each is checked."""
    for c in CODES:
        other = sorted(set(u for o in CODES if o != c for u in status_uris(o)))
        own = status_uris(c)
        r = rule(f"tw_ext_{c}_status_code_mismatch", "name", CLASSIFICATION,
                 "Statuses belong to the name's code", "absence", "hard", "error",
                 [code_is(c), has_status(other)], {},
                 f"A status on this name belongs to another nomenclatural code; "
                 f"{CODES[c]['label']} names need {CODES[c]['label']} statuses.", 15,
                 f"TaxonNameClassification#nomenclature_code_matches ({TNC})")
        add(c, r, [("other code's status", srow(c, [own[0], other[0]]))],
            [("own statuses", srow(c, own[:2]))], ext=True,
            needs=[f"name.{CLASSIFICATION}"], supersedes=[f"tw_{c}_status_code_mismatch"])
    # Rank applicability and year windows are ICZN-only, as in the core
    # bundles; the other codes allow every status at every rank of the code.
    c = "iczn"
    groups = {
        "family": ("FAMILY_RANK_NAMES_ICZN", group(c, "family"), "FAMILY", "SPECIES", "family-group"),
        "genus": ("GENUS_RANK_NAMES_ICZN", group(c, "genus"), "GENUS", "FAMILY", "genus-group"),
        "species": ("SPECIES_RANK_NAMES_ICZN", group(c, "species"), "SPECIES", "GENUS", "species-group"),
        "above_species": ("FAMILY_AND_ABOVE_RANK_NAMES + GENUS_RANK_NAMES_ICZN",
                          group(c, "higher", "family", "genus"), "GENUS", "SPECIES", "genus-group or higher"),
    }
    extra = {"species": ["Iczn::Unavailable::NonBinominal::SpeciesNotBinominal",
                         "Iczn::Unavailable::NonBinominal::SubspeciesNotTrinominal"]}
    for g, (const, ranks, good_rank, bad_rank, what) in groups.items():
        us = sorted(set(uris(classes(c, lambda x, k=const: x["applicable_ranks_constant"] == k))
                        + by_class(*extra.get(g, []))))
        r = rule(f"tw_ext_iczn_status_rank_{g}", "name", "col__rank_id",
                 f"Status applies to {what} names only", "regex", "soft", "warn",
                 [code_is(c), has_status(us), nonempty("col__rank_id")], {"pattern": alt(ranks)},
                 f"A nomenclatural status on this name applies only to {what} names.", 30,
                 f"TaxonNameClassification#sv_proper_classification ({TNC}) with applicable_ranks")
        add(c, r, [(f"status on {bad_rank}", srow(c, [us[0]], bad_rank))],
            [(f"status on {good_rank}", srow(c, [us[0]], good_rank))], ext=True,
            needs=[f"name.{CLASSIFICATION}"], supersedes=[f"tw_iczn_status_rank_{g}"])
    win = {}
    for x in classes(c):
        s, e = x["code_applicability_start_year"], x["code_applicability_end_year"]
        if s > e:
            s, e = None, 1757
        win.setdefault((s, e), []).append(x)
    for (s, e), cs in sorted(win.items(), key=lambda k: (k[0][0] or 0, k[0][1])):
        us = uris(cs)
        params = {}
        if s and s > 1:
            params["min"] = s
        if e and e < 9999:
            params["max"] = e
        if (s, e) == (1758, 9999):
            key, rname = "after_1757", "Status used on a name published after 1757"
            msg = "ICZN statuses apply to names published in 1758 or later."
        elif s is None:
            key, rname = "pre_linnean", "Pre-Linnean status on a name published before 1758"
            msg = "The pre-Linnean status applies only to names published before 1758."
        else:
            span = f"{s}–{e}" if e < 9999 else f"{s} or later"
            key = f"{s}_{e}" if e < 9999 else f"{s}_on"
            rname = f"Status limited to names published {span}"
            msg = f"A status on this name applies only to names published {'in ' if e < 9999 else ''}{span}."
        r = rule(f"tw_ext_iczn_status_year_{key}", "name", "col__published_in_year", rname, "range",
                 "soft", "warn",
                 [code_is(c), has_status(us), cond("col__published_in_year", "matches", "^[0-9]{4}$")],
                 params, msg + " Check the year and the statuses.", 30,
                 f"TaxonNameClassification#sv_proper_year ({TNC})")
        bad = str((s - 1) if s and s > 1 else (e + 1))
        good = str(s if s and s > 1 else e)
        add(c, r, [(f"year {bad}", srow(c, [us[0]], "SPECIES", bad))],
            [(f"year {good}", srow(c, [us[0]], "SPECIES", good))], ext=True,
            needs=[f"name.{CLASSIFICATION}"], supersedes=[f"tw_iczn_status_year_{key}"])
    skip = {P + "Iczn::Available::Valid", P + "Iczn::Available::Invalid", P + "Iczn::Available::Invalid::Homonym"}
    for c in CODES:
        cs = [x for x in classes(c) if x["not_specific_defined_by"] == x["class"] and x["class"] not in skip]
        if not cs:
            continue
        us = uris(cs)
        r = rule(f"tw_ext_{c}_status_not_specific", "name", CLASSIFICATION, "More specific status preferred",
                 "absence", "soft", "info", [code_is(c), has_status(us)], {},
                 "A status on this name has more specific sub-statuses. Choose the one that states the reason.",
                 40, f"TaxonNameClassification#sv_not_specific_classes ({TNC})")
        specific = [u for u in status_uris(c) if u not in us]
        add(c, r, [("general status", srow(c, [us[0]]))], [("specific status", srow(c, [specific[0]]))],
            ext=True, needs=[f"name.{CLASSIFICATION}"], supersedes=[f"tw_{c}_status_not_specific"])
    us = by_class("Iczn::Available::Invalid", "Iczn::Available::Invalid::Homonym")
    r = rule("tw_ext_iczn_status_prefer_relationship", "name", CLASSIFICATION,
             "Relationship preferred over status", "absence", "soft", "info",
             [code_is("iczn"), has_status(us)], {},
             "A status on this name can be used, but a name relationship records the reason more precisely.",
             40, "TaxonNameClassification::Iczn::Available::Invalid#sv_not_specific_classes "
             "(app/models/taxon_name_classification/iczn/available/invalid.rb)")
    add("iczn", r, [("invalid status", srow("iczn", us))],
        [("nomen nudum status", srow("iczn", by_class("Iczn::Unavailable::NomenNudum")))], ext=True,
        needs=[f"name.{CLASSIFICATION}"], supersedes=["tw_iczn_status_prefer_relationship"])
    r = rule("tw_ext_status_code_root", "name", CLASSIFICATION, "Statuses are specific", "absence", "hard",
             "error", [has_status(CODE_ROOTS)], {},
             "A status on this name is only the name of a nomenclatural code. Choose specific statuses.", 15,
             f"TaxonNameClassification#validate_taxon_name_classification ({TNC})")
    add(None, r, [("code root as status", srow("iczn", [CODE_ROOTS[0]]))],
        [("specific status", srow("iczn", [status_uris("iczn")[0]]))], ext=True,
        needs=[f"name.{CLASSIFICATION}"], supersedes=["tw_status_code_root"])
    lat = latinized()
    r = rule("tw_ext_status_is_grammatical_term", "name", CLASSIFICATION,
             "Statuses are not grammatical terms", "absence", "soft", "warn", [has_status(lat)], {},
             "A status on this name is a grammatical gender or part-of-speech term, which is not a "
             "nomenclatural status. Record gender in the gender field.", 30,
             "TaxonNameClassification::Latinized (app/models/taxon_name_classification/latinized.rb)")
    add(None, r, [("gender term as status", srow("iczn", lat[:1]))],
        [("nomenclatural status", srow("iczn", [status_uris("iczn")[0]]))], ext=True,
        needs=[f"name.{CLASSIFICATION}"], supersedes=["tw_status_is_grammatical_term"])


CONFLICTS = {
    "iczn": [("available and unavailable", "Iczn::Available", "Iczn::Unavailable"),
             ("valid and invalid", "Iczn::Available::Valid", "Iczn::Available::Invalid")],
    "icn": [("effectively and not effectively published", "Icn::EffectivelyPublished",
             "Icn::NotEffectivelyPublished"),
            ("validly and invalidly published", "Icn::EffectivelyPublished::ValidlyPublished",
             "Icn::EffectivelyPublished::InvalidlyPublished"),
            ("legitimate and illegitimate", "Icn::EffectivelyPublished::ValidlyPublished::Legitimate",
             "Icn::EffectivelyPublished::ValidlyPublished::Illegitimate")],
    "icnp": [("effectively and not effectively published", "Icnp::EffectivelyPublished",
              "Icnp::NotEffectivelyPublished"),
             ("validly and invalidly published", "Icnp::EffectivelyPublished::ValidlyPublished",
              "Icnp::EffectivelyPublished::InvalidlyPublished"),
             ("legitimate and illegitimate", "Icnp::EffectivelyPublished::ValidlyPublished::Legitimate",
              "Icnp::EffectivelyPublished::ValidlyPublished::Illegitimate")],
    "icvcn": [("valid and invalid", "Icvcn::Valid", "Icvcn::Invalid"),
              ("accepted and unaccepted", "Icvcn::Valid::Accepted", "Icvcn::Valid::Unaccepted")],
}


def status_round_trip_guard():
    """The ext status rules supersede the core ones, which read the single
    col__status_id. On an archive that carries the round-trip columns but
    a status only in col__status_id, neither would check it — say so
    rather than pass silently."""
    r = rule("tw_ext_status_not_round_tripped", "name", CLASSIFICATION,
             "Status is recorded in the round-trip column", "presence", "soft", "warn",
             [nonempty("col__status_id")], {},
             "This name has a nomenclatural status but no round-tripped classification, so the status "
             "rules did not check it. Record the status as a NOMEN classification.", 30,
             f"TaxonNameClassification ({TNC}); hive reads the round-trip column where it exists")
    add(None, r,
        [("status only in col__status_id",
          [name("n1", "Aus bus", col__status_id=status_uris("iczn")[0])])],
        [("status round-tripped",
          [name("n1", "Aus bus", col__status_id=status_uris("iczn")[0],
                **{CLASSIFICATION: status_uris("iczn")[0]})]),
         ("no status", [name("n1", "Aus bus")])],
        ext=True, needs=[f"name.{CLASSIFICATION}"])


def conflicting_statuses():
    """TaxonNameClassification#sv_validate_disjoint_classes: two statuses
    on one name that exclude each other."""
    for c, pairs in CONFLICTS.items():
        for what, a, b in pairs:
            ua, ub = descendants_and_self(a), descendants_and_self(b)
            both = f"{pat(ua)}.*{pat(ub)}|{pat(ub)}.*{pat(ua)}"
            key = what.replace(" ", "_").replace("and_", "")
            r = rule(f"tw_ext_{c}_conflicting_status_{key}", "name", CLASSIFICATION,
                     f"Conflicting statuses: {what}", "absence", "soft", "warn",
                     [code_is(c), cond(CLASSIFICATION, "matches", both)], {},
                     f"This name carries statuses that exclude each other ({what}). Keep the one that applies.",
                     30, f"TaxonNameClassification#sv_validate_disjoint_classes ({TNC}) with "
                     "disjoint_taxon_name_classes")
            add(c, r, [("both statuses", srow(c, [ua[0], ub[0]]))], [("one status", srow(c, [ua[0]]))],
                ext=True, needs=[f"name.{CLASSIFICATION}"])


# ------------------------------------------------------- name refinements

def name_refinements():
    r = rule("tw_ext_rank_required", "name", "col__rank_id", "Rank is recorded for a protonym", "presence",
             "hard", "error", [cond("tw__name_class", "not_equals", "Combination")], {},
             "Every protonym needs a rank.", 15,
             "Protonym validates_presence_of :rank_class (app/models/protonym.rb)")
    add(None, r, [("protonym without rank", [name("n1", "Aus", tw__name_class="Protonym")]),
                  ("name class unknown, no rank", [name("n1", "Aus")])],
        [("protonym with rank", [name("n1", "Aus", tw__name_class="Protonym", col__rank_id="GENUS")]),
         ("combination without rank", [name("n1", "Aus bus", tw__name_class="Combination")])],
        ext=True, needs=["name.tw__name_class"], supersedes=["tw_rank_required"])
    r = rule("tw_ext_verbatim_author_without_digits", "name", "tw__verbatim_author",
             "Author contains no digits", "regex", "hard", "error", [nonempty("tw__verbatim_author")],
             {"pattern": "^[^0-9]*$"},
             "The author must not contain digits; the year belongs in its own field.", 20,
             "Protonym#verbatim_author_without_digits (app/models/protonym.rb)")
    add(None, r, [("author with year", [name("n1", "Aus", tw__verbatim_author="Smith 1900")])],
        [("author only", [name("n1", "Aus", tw__verbatim_author="Smith")])], ext=True,
        needs=["name.tw__verbatim_author"],
        supersedes=["tw_combination_authorship_without_digits", "tw_basionym_authorship_without_digits"])
    for c in ("iczn", "icvcn"):
        cid = CODES[c]["id"]
        r = rule(f"tw_ext_{c}_authorship_parentheses", "name", "tw__verbatim_author",
                 "Author parentheses are closed", "regex", "hard", "error",
                 [code_is(c), nonempty("tw__verbatim_author")], {"pattern": "^(?:\\([^()]+\\)|[^()]+)$"},
                 "The author must either be wholly enclosed in one pair of parentheses or contain none.", 20,
                 "Protonym#verbatim_author_with_closed_parens_when_present (app/models/protonym.rb)")
        add(c, r, [("unclosed", [name("n1", "Aus", col__code_id=cid, tw__verbatim_author="(Smith")])],
            [("closed", [name("n1", "Aus", col__code_id=cid, tw__verbatim_author="(Smith)")])], ext=True,
            needs=["name.tw__verbatim_author"], supersedes=[f"tw_{c}_authorship_parentheses"])
    r = rule("tw_ext_year_of_publication_integer", "name", "tw__year_of_publication",
             "Year of publication is an integer", "regex", "hard", "error",
             [nonempty("tw__year_of_publication")], {"pattern": "^[0-9]+$"},
             "The year of publication must be a whole number.", 15,
             "TaxonName validates :year_of_publication, date_year (app/models/taxon_name.rb)")
    add(None, r, [("1900a", [name("n1", "Aus", tw__year_of_publication="1900a")])],
        [("1900", [name("n1", "Aus", tw__year_of_publication="1900")])], ext=True,
        needs=["name.tw__year_of_publication"])
    r = rule("tw_ext_year_of_publication_range", "name", "tw__year_of_publication",
             "Year of publication is plausible", "range", "hard", "error",
             [cond("tw__year_of_publication", "matches", "^[0-9]+$")],
             {"min": 1000, "max": "current_year"},
             "The year of publication must be between 1000 and the current year.", 15,
             "TaxonName validates :year_of_publication, date_year (app/models/taxon_name.rb)")
    add(None, r, [("999", [name("n1", "Aus", tw__year_of_publication="999")])],
        [("1900", [name("n1", "Aus", tw__year_of_publication="1900")])], ext=True,
        needs=["name.tw__year_of_publication"])
    for c in CODES:
        cid = CODES[c]["id"]
        ranks = group(c, "family", "genus", "species")
        r = rule(f"tw_ext_{c}_missing_author_links", "name", "tw__author_ids",
                 "Name authors are linked", "presence", "soft", "info", [code_is(c), rank_in(ranks)], {},
                 "The authors of this name are not linked to author records.", 50,
                 f"Protonym#sv_missing_roles ({SVE})")
        add(c, r, [("unlinked", [name("n1", "Aus", col__code_id=cid, col__rank_id="GENUS")])],
            [("linked", [name("n1", "Aus", col__code_id=cid, col__rank_id="GENUS", tw__author_ids="a1")])],
            ext=True, needs=["name.tw__author_ids"],
            supersedes=[f"tw_{c}_missing_author_links", f"tw_{c}_missing_original_author_links"])
        r = rule(f"tw_ext_{c}_missing_original_publication", "name", "tw__reference_original",
                 "Missing original publication", "presence", "soft", "info",
                 [code_is(c), rank_in(ranks)], {},
                 "No citation of this name is recorded as the original publication.", 50,
                 "TaxonName#sv_missing_original_publication (app/models/taxon_name.rb)")
        add(c, r, [("no original citation",
                    [name("n1", "Aus", col__code_id=cid, col__rank_id="GENUS", tw__reference_ids="r1")])],
            [("original flagged", [name("n1", "Aus", col__code_id=cid, col__rank_id="GENUS",
                                        tw__reference_ids="r1", tw__reference_original="r1")])],
            ext=True, needs=["name.tw__reference_original"],
            supersedes=[f"tw_{c}_missing_original_publication"])
    r = rule("tw_ext_redundant_verbatim_name", "name", "tw__verbatim_name",
             "Verbatim name differs from the computed name", "absence", "soft", "warn",
             [nonempty("tw__verbatim_name"),
              cond("tw__verbatim_name", "equals", {"field": "col__scientific_name"})], {},
             "The verbatim name is the same as the computed name, so it is not needed.", 30,
             "Combination#sv_redundant_verbatim_name (app/models/combination.rb)")
    add(None, r, [("same", [name("n1", "Aus bus", tw__verbatim_name="Aus bus")])],
        [("different", [name("n1", "Aus bus", tw__verbatim_name="Aus bús")])], ext=True,
        needs=["name.tw__verbatim_name"])
    r = rule("tw_ext_combination_duplicate", "name", "col__scientific_name", "Duplicate combination",
             "count_across", "soft", "warn", [cond("tw__name_class", "equals", "Combination")],
             {"table": "name", "match": [{"target": "col__scientific_name", "self": "col__scientific_name"},
                                         {"target": "tw__verbatim_name", "self": "tw__verbatim_name"}],
              "exclude_self": True, "expected": {"max": 0}, "skip_if_any_self_empty": False},
             "Another combination has the same name and verbatim name.", 30,
             "Combination#is_unique and #sv_combination_duplicates (app/models/combination.rb)")
    add(None, r, [("two identical combinations",
                   [name("c1", "Aus bus", tw__name_class="Combination"),
                    name("c2", "Aus bus", tw__name_class="Combination")])],
        [("one combination", [name("c1", "Aus bus", tw__name_class="Combination")])], ext=True,
        needs=["name.tw__name_class", "name.tw__verbatim_name"])


# ------------------------------------------------------------ type material

LEGAL_TW_TYPES = {
    "iczn": ["holotype", "paratype", "paralectotype", "neotype", "lectotype", "syntype", "paratypes",
             "syntypes", "paralectotypes"],
    "icn": ["holotype", "paratype", "lectotype", "neotype", "epitype", "isotype", "syntype", "isosyntype",
            "syntypes", "isotypes", "isosyntypes"],
}


def type_material():
    for c, legal in LEGAL_TW_TYPES.items():
        cid = CODES[c]["id"]
        r = rule(f"tw_ext_{c}_type_material_status", "type_material", "tw__type_type",
                 f"Type status is used by the {CODES[c]['label']}", "regex", "hard", "error",
                 [nonempty("tw__type_type"), cond("col__code_id", "equals", cid, "type_material:name")],
                 {"pattern": alt(legal)},
                 f"This type status is not one the {CODES[c]['label']} uses.", 15,
                 "TypeMaterial#check_type_type (app/models/type_material.rb)")
        n = name("n1", "Aus bus", col__code_id=cid, col__rank_id="SPECIES")
        bad = "epitype" if c == "iczn" else "paralectotype"
        lot = "paratypes" if c == "iczn" else "syntypes"
        add(c, r, [(bad, [n, tm("n1", "", tw__type_type=bad)])],
            [("holotype", [n, tm("n1", "", tw__type_type="holotype")]),
             (f"{lot} (a lot)", [n, tm("n1", "", tw__type_type=lot, tw__total="5")])],
            ext=True, needs=["type_material.tw__type_type"],
            supersedes=[f"tw_{c}_type_material_status"])


# -------------------------------------------------------------- relations

OC_CHAIN = [("OriginalSubform", "OriginalForm"), ("OriginalForm", "OriginalSpecies"),
            ("OriginalSubvariety", "OriginalVariety"), ("OriginalVariety", "OriginalSpecies"),
            ("OriginalSubspecies", "OriginalSpecies"), ("OriginalSpecies", "OriginalGenus"),
            ("OriginalSubgenus", "OriginalGenus")]
CB_CHAIN = [("Subform", "Form"), ("Form", "Species"), ("Subvariety", "Variety"), ("Variety", "Species"),
            ("Subspecies", "Species"), ("Species", "Genus"), ("Subgenus", "Genus"),
            ("Subsection", "Section"), ("Section", "Genus"), ("Subseries", "Series"), ("Series", "Genus")]


def human_element(e):
    out = ""
    for ch in e.replace("Original", ""):
        out += (" " + ch.lower()) if ch.isupper() and out else ch.lower()
    return out


def combination_elements():
    """TaxonNameRelationship#sv_validate_required_relationships: an element
    of a combination requires the elements above it. Each rule checks the
    next element up, so an incomplete chain is reported at the gap."""
    t = f"TaxonNameRelationship#sv_validate_required_relationships ({TNRF}.rb) with " \
        "required_taxon_name_relationships"
    for chain, prefix, kind in ((OC_CHAIN, OC, "original combination"), (CB_CHAIN, CB, "combination")):
        for have, need in chain:
            rid = f"tw_ext_{'oc' if prefix == OC else 'combination'}_{have.lower()}_needs_{need.lower()}"
            r = rule(rid, "name", "col__id", f"{human_element(have).capitalize()} needs a "
                     f"{human_element(need)}", "foreign_key_exists", "soft", "warn",
                     [cond("col__name_id", "exists", None, f"name:element-{have.lower()}")],
                     {"field": "col__id", "relation": f"name:element-{need.lower()}"},
                     f"This {kind} records a {human_element(have)} but no {human_element(need)}. "
                     "Record every element of the combination.", 30, t)
            base = [name("n1", "Aus bus"), name("e1", "Bus"), rel("e1", "n1", "", **{NR_TYPE: prefix + have})]
            good = base + [name("e2", "Cus"), rel("e2", "n1", "", **{NR_TYPE: prefix + need})]
            add(None, r, [(f"{have} without {need}", base)], [(f"{have} with {need}", good)], ext=True,
                needs=[f"name_relation.{NR_TYPE}"])


URI = {
    "forgotten": "http://purl.obolibrary.org/obo/NOMEN_0000106",
    "secondary1961": "http://purl.obolibrary.org/obo/NOMEN_0000292",
    "family_before_1961": "http://purl.obolibrary.org/obo/NOMEN_0000042",
    "replacement": "http://purl.obolibrary.org/obo/NOMEN_0000270",
    "replaced_homonym": "http://purl.obolibrary.org/obo/NOMEN_0000112",
    "unnecessary_replacement": "http://purl.obolibrary.org/obo/NOMEN_0000279",
    "total_suppression": "http://purl.obolibrary.org/obo/NOMEN_0000283",
    "homonym": "http://purl.obolibrary.org/obo/NOMEN_0000289",
    "invalidating": "http://purl.obolibrary.org/obo/NOMEN_0000272",
    "synonym_iczn": "http://purl.obolibrary.org/obo/NOMEN_0000276",
    "suppression": "http://purl.obolibrary.org/obo/NOMEN_0000280",
    "synonym_icn": "http://purl.obolibrary.org/obo/NOMEN_0000372",
    "synonym_icnp": "http://purl.obolibrary.org/obo/NOMEN_0000096",
}
SUBSEQUENT = {"designation": TYPIFICATION + "Genus::Subsequent::SubsequentDesignation",
              "monotypy": TYPIFICATION + "Genus::Subsequent::SubsequentMonotypy"}


def relationship_years():
    """Year thresholds that TaxonWorks checks on a relationship's subject
    or object, now that the relationship's own class is recorded."""
    c = "iczn"
    cid = CODES[c]["id"]
    for key, rid, rname, limit, what, tw in [
        ("forgotten", "tw_ext_iczn_forgotten_homonym_year", "Forgotten homonym was described before 1900",
         1899, "A forgotten name (nomen oblitum) must have been described before 1900.",
         f"TaxonNameRelationship::Iczn::Invalidating::Homonym::Primary::Forgotten#sv_forgotten_homonym "
         f"({TNRF}/iczn/invalidating/homonym/primary/forgotten.rb)"),
        ("secondary1961", "tw_ext_iczn_secondary_homonym_1961_year",
         "Secondary homonym replaced before 1961 was described before 1961", 1960,
         "A secondary homonym replaced before 1961 must have been described before 1961.",
         f"TaxonNameRelationship::Iczn::Invalidating::Homonym::Secondary::Secondary1961#sv_year_of_description "
         f"({TNRF}/iczn/invalidating/homonym/secondary/secondary1961.rb)"),
        ("family_before_1961", "tw_ext_iczn_family_before_1961_year",
         "Family-group name replaced before 1961 was described before 1961", 1960,
         "A family-group name based on a genus synonym replaced before 1961 must have been described "
         "before 1961.",
         f"TaxonNameRelationship::Iczn::PotentiallyValidating::FamilyBefore1961#sv_year_of_description "
         f"({TNRF}/iczn/potentially_validating/family_before1961.rb)"),
    ]:
        r = rule(rid, "name", "col__published_in_year", rname, "range", "soft", "warn",
                 [code_is(c), cond("col__published_in_year", "matches", "^[0-9]{4}$"),
                  cond("col__name_id", "exists", None, f"name:{key.replace('_', '-')}-relation")],
                 {"max": limit}, what + " Check the year or the relationship.", 30, tw)
        rows = lambda y: [name("n1", "Aus bus", col__code_id=cid, col__published_in_year=y),
                          name("n2", "Aus bus", col__code_id=cid),
                          rel("n1", "n2", "", **{NR_TYPE: URI[key]})]
        add(c, r, [(f"year {limit + 20}", rows(str(limit + 20)))], [(f"year {limit - 10}", rows(str(limit - 10)))],
            ext=True, needs=[f"name_relation.{NR_TYPE}"])
    for key, cls in SUBSEQUENT.items():
        rid = f"tw_ext_iczn_subsequent_{key}_after_1930"
        r = rule(rid, "name", "col__published_in_year",
                 f"Genus with a subsequent {key} was described before 1931", "range", "soft", "warn",
                 [code_is(c), cond("col__published_in_year", "matches", "^[0-9]{4}$"),
                  cond("col__name_id", "exists", None, f"name:subsequent-{key}-of")],
                 {"max": 1930},
                 f"This genus was described after 1930, so it is a nomen nudum unless its type was "
                 "designated in the original publication.", 30,
                 f"TaxonNameRelationship::Typification::Genus::Subsequent::Subsequent{key.capitalize()}"
                 f"#sv_described_after_1930 ({TNRF}/typification/genus/subsequent/subsequent_{key}.rb)")
        rows = lambda y: [name("g", "Aus", col__code_id=cid, col__rank_id="GENUS", col__published_in_year=y),
                          name("s", "Aus bus", col__code_id=cid, col__rank_id="SPECIES"),
                          rel("s", "g", "TYPE", **{NR_TYPE: cls})]
        add(c, r, [("genus 1950", rows("1950"))], [("genus 1900", rows("1900"))], ext=True,
            needs=[f"name_relation.{NR_TYPE}"])


def relationship_structure():
    c = "iczn"
    cid = CODES[c]["id"]
    r = rule("tw_ext_iczn_homonym_of_suppressed_name", "name", "col__id",
             "Homonym of a totally suppressed name", "absence", "soft", "warn",
             [code_is(c), cond("col__name_id", "exists", None, "name:homonym-of-suppressed")], {},
             "The name this one is a homonym of is totally suppressed, so this name should not be "
             "treated as a homonym.", 30,
             f"TaxonNameRelationship::Iczn::Invalidating::Homonym#sv_validate_total_suppression "
             f"({TNRF}/iczn/invalidating/homonym.rb)")
    base = [name("j", "Aus bus", col__code_id=cid), name("s", "Aus bus", col__code_id=cid),
            rel("j", "s", "LATER_HOMONYM", **{NR_TYPE: URI["homonym"]})]
    add(c, r, [("senior suppressed", base + [name("v", "Aus cus", col__code_id=cid),
                                             rel("s", "v", "", **{NR_TYPE: URI["total_suppression"]})])],
        [("senior not suppressed", base)], ext=True, needs=[f"name_relation.{NR_TYPE}"])
    r = rule("tw_ext_iczn_replacement_needs_objective_synonym", "name", "col__id",
             "Replacement name has an objective synonym relationship", "foreign_key_exists", "soft", "warn",
             [code_is(c), cond("col__name_id", "exists", None, "name:replacement-for"),
              cond("col__name_id", "not_exists", None, "name:unnecessary-replacement")],
             {"field": "col__id", "relation": "name:replaced-homonym-incoming"},
             "This name replaces another, but neither a replaced-homonym nor an unnecessary-replacement "
             "relationship records why. Add one.", 30,
             f"TaxonNameRelationship::Iczn::PotentiallyValidating::ReplacementName#sv_specific_relationship "
             f"({TNRF}/iczn/potentially_validating/replacement_name.rb)")
    repl = [name("n", "Aus cus", col__code_id=cid), name("h", "Aus bus", col__code_id=cid),
            rel("n", "h", "REPLACEMENT_NAME", **{NR_TYPE: URI["replacement"]})]
    add(c, r, [("replacement alone", repl)],
        [("with replaced homonym", repl + [rel("h", "n", "", **{NR_TYPE: URI["replaced_homonym"]})]),
         ("with unnecessary replacement", repl + [rel("n", "h", "", **{NR_TYPE: URI["unnecessary_replacement"]})])],
        ext=True, needs=[f"name_relation.{NR_TYPE}"])
    r = rule("tw_ext_iczn_replacement_of_replacement", "name", "col__id",
             "Replacement name of a replacement name", "absence", "soft", "warn",
             [code_is(c), cond("col__name_id", "exists", None, "name:replacement-for"),
              cond("col__name_id", "exists", None, "name:replaced-homonym-incoming")], {},
             "A replacement name cannot be proposed for another replacement name. Move the relationship "
             "to the older objective synonym.", 30,
             "TaxonNameRelationship::Iczn::Invalidating::Synonym::Objective::ReplacedHomonym"
             f"#sv_specific_relationship ({TNRF}/iczn/invalidating/synonym/objective/replaced_homonym.rb)")
    chain = [name("n", "Aus cus", col__code_id=cid), name("h", "Aus bus", col__code_id=cid),
             rel("n", "h", "REPLACEMENT_NAME", **{NR_TYPE: URI["replacement"]}),
             rel("h", "n", "", **{NR_TYPE: URI["replaced_homonym"]})]
    add(c, r, [("replacement of a replacement", chain)],
        [("plain replacement", chain[:3])], ext=True, needs=[f"name_relation.{NR_TYPE}"])


GENERIC = [
    (None, "tw_ext_typification_not_specific", [TYPIFICATION + "Genus", TYPIFICATION + "Genus::Original",
                                                TYPIFICATION + "Genus::Subsequent",
                                                TYPIFICATION + "Genus::Tautonomy"],
     "Type designation is not specific",
     "Record how the type was designated: original designation or monotypy, subsequent designation or "
     "monotypy, or tautonomy.",
     f"TaxonNameRelationship::Typification::Genus#sv_not_specific_relationship "
     f"({TNRF}/typification/genus.rb)"),
    ("iczn", "tw_ext_iczn_invalidating_not_specific", [URI["invalidating"]],
     "Invalidating relationship is not specific",
     "Record why the name is unavailable or invalid, rather than the general relationship.",
     f"TaxonNameRelationship::Iczn::Invalidating#sv_not_specific_relationship "
     f"({TNRF}/iczn/invalidating.rb)"),
    ("iczn", "tw_ext_iczn_synonym_not_specific", [URI["synonym_iczn"]],
     "Synonym relationship is not specific",
     "Record whether this is an objective or a subjective synonym.",
     f"TaxonNameRelationship::Iczn::Invalidating::Synonym#sv_not_specific_relationship "
     f"({TNRF}/iczn/invalidating/synonym.rb)"),
    ("iczn", "tw_ext_iczn_suppression_not_specific", [URI["suppression"]],
     "Suppression is not specific",
     "Record whether the suppression is total, partial or conditional.",
     f"TaxonNameRelationship::Iczn::Invalidating::Synonym::Suppression#sv_not_specific_relationship "
     f"({TNRF}/iczn/invalidating/synonym/suppression.rb)"),
    ("icn", "tw_ext_icn_synonym_not_specific", [URI["synonym_icn"]],
     "Synonym relationship is not specific",
     "Record whether this is a homotypic or a heterotypic synonym.",
     f"TaxonNameRelationship::Icn::Unaccepting::Synonym#sv_not_specific_relationship "
     f"({TNRF}/icn/unaccepting/synonym.rb)"),
    ("icnp", "tw_ext_icnp_synonym_not_specific", [URI["synonym_icnp"]],
     "Synonym relationship is not specific",
     "Record whether this is a homotypic or a heterotypic synonym.",
     f"TaxonNameRelationship::Icnp::Unaccepting::Synonym#sv_not_specific_relationship "
     f"({TNRF}/icnp/unaccepting/synonym.rb)"),
]


def generic_relationships():
    """The "not specific relationship" family: a relationship recorded at
    the general class where a subclass says why."""
    for code, rid, values, rname, msg, tw in GENERIC:
        r = rule(rid, "name_relation", NR_TYPE, rname, "absence", "soft", "info",
                 [cond(NR_TYPE, "in", values)], {}, msg, 40, tw)
        specific = {URI["invalidating"]: URI["synonym_iczn"], URI["synonym_iczn"]: URI["replaced_homonym"],
                    URI["suppression"]: URI["total_suppression"],
                    URI["synonym_icn"]: "http://purl.obolibrary.org/obo/NOMEN_0000393",
                    URI["synonym_icnp"]: "http://purl.obolibrary.org/obo/NOMEN_0000101",
                    TYPIFICATION + "Genus": TYPIFICATION + "Genus::Original::OriginalDesignation"}[values[0]]
        rows = lambda v: [name("a", "Aus"), name("b", "Bus"), rel("a", "b", "", **{NR_TYPE: v})]
        add(code, r, [("general class", rows(values[0]))], [("specific class", rows(specific))], ext=True,
            needs=[f"name_relation.{NR_TYPE}"])


def original_genus():
    """Protonym#sv_missing_original_genus, now that the original-combination
    elements travel."""
    for c in CODES:
        cid = CODES[c]["id"]
        r = rule(f"tw_ext_{c}_missing_original_genus", "name", "col__id", "Missing original genus",
                 "foreign_key_exists", "soft", "info",
                 [code_is(c), rank_in(group(c, "genus", "species"))],
                 {"field": "col__id", "relation": "name:element-originalgenus"},
                 "No original genus is recorded for this name.", 50,
                 f"Protonym#sv_missing_original_genus ({SVE})")
        base = [name("n1", "Aus bus", col__code_id=cid, col__rank_id="SPECIES")]
        add(c, r, [("no original genus", base)],
            [("original genus", base + [name("g", "Aus", col__code_id=cid),
                                        rel("g", "n1", "", **{NR_TYPE: OC + "OriginalGenus"})])],
            ext=True, needs=[f"name_relation.{NR_TYPE}"])


def otus():
    """tw__otu carries the OTUs a name has; SFGA keeps one taxon per name."""
    r = rule("tw_ext_duplicate_otu", "tw__otu", "name_id", "Duplicate OTU for a name", "count_across",
             "soft", "info", None,
             {"table": "tw__otu", "match": [{"target": "name_id", "self": "name_id"},
                                            {"target": "name", "self": "name"}],
              "exclude_self": True, "expected": {"max": 0}, "skip_if_any_self_empty": False},
             "Another OTU has the same name and label. Merge them or give one a label.", 40,
             "Otu#sv_duplicate_otu (app/models/otu.rb)")
    otu = lambda i, nid, label="": {"table": "tw__otu", "values": {"id": i, "name_id": nid, "name": label}}
    add(None, r, [("two unlabelled OTUs", [name("n1", "Aus bus"), otu("o1", "n1"), otu("o2", "n1")])],
        [("one labelled", [name("n1", "Aus bus"), otu("o1", "n1"), otu("o2", "n1", "n. sp. T1")])],
        ext=True, needs=["tw__otu"], supersedes=["tw_duplicate_taxon"])
    r = rule("tw_ext_otu_without_name", "tw__otu", "name_id", "OTU has a name or a label", "presence",
             "soft", "warn", [cond("name", "equals", "")], {},
             "This OTU has neither a taxon name nor a label of its own.", 30,
             "Otu#check_required_fields (app/models/otu.rb)")
    add(None, r, [("empty OTU", [otu("o1", "")])],
        [("OTU with a name", [name("n1", "Aus bus"), otu("o1", "n1")]),
         ("OTU with a label", [otu("o1", "", "n. sp. T1")])], ext=True, needs=["tw__otu"])
    r = rule("tw_ext_missing_otu", "name", "col__id", "Missing OTU", "foreign_key_exists", "soft", "info",
             [nonempty("col__rank_id")], {"field": "col__id", "relation": "name:otu"},
             "No OTU is recorded for this name, which TaxonWorks needs to hang data off it.", 50,
             f"Protonym#sv_missing_otu ({SVE})")
    add(None, r, [("name without OTU", [name("n1", "Aus bus", col__rank_id="SPECIES")])],
        [("name with OTU", [name("n1", "Aus bus", col__rank_id="SPECIES"), otu("o1", "n1")])], ext=True,
        needs=["tw__otu"])


def build():
    status_rules()
    status_round_trip_guard()
    conflicting_statuses()
    name_refinements()
    type_material()
    combination_elements()
    relationship_years()
    relationship_structure()
    generic_relationships()
    original_genus()
    otus()
