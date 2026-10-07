"""Nomenclatural status rules (TaxonNameClassification). SFGA stores one
status per name in name.col__status_id as a NOMEN URI."""
from lib import *
from twdata import *
from placement import human, listing

TNC = "app/models/taxon_name_classification.rb"


def srow(code, status, rank="SPECIES", year="", cid=None):
    v = {"col__code_id": cid or CODES[code]["id"], "col__rank_id": rank, "col__status_id": status}
    if year:
        v["col__published_in_year"] = year
    return [name(uid("n"), "Aus bus", **v)]


def code_mismatch(code):
    other = sorted(set(u for c in CODES if c != code for u in status_uris(c)))
    own = status_uris(code)
    r = rule(f"tw_{code}_status_code_mismatch", "name", "col__status_id", "Status belongs to the name's code",
             "absence", "hard", "error", [code_is(code), cond("col__status_id", "in", other)], {},
             f"This status belongs to another nomenclatural code; {CODES[code]['label']} names need an "
             f"{CODES[code]['label']} status.", 15,
             f"TaxonNameClassification#nomenclature_code_matches ({TNC})")
    add(code, r, [("other code's status", srow(code, other[0]))], [("own status", srow(code, own[0]))])


def rank_applicability():
    """TaxonNameClassification#sv_proper_classification for the ICZN
    statuses restricted to one rank group."""
    c = "iczn"
    t = f"TaxonNameClassification#sv_proper_classification ({TNC}) with applicable_ranks"
    groups = {
        "family": ("FAMILY_RANK_NAMES_ICZN", group(c, "family"), "FAMILY", "SPECIES"),
        "genus": ("GENUS_RANK_NAMES_ICZN", group(c, "genus"), "GENUS", "FAMILY"),
        "species": ("SPECIES_RANK_NAMES_ICZN", group(c, "species"), "SPECIES", "GENUS"),
        "above_species": ("FAMILY_AND_ABOVE_RANK_NAMES + GENUS_RANK_NAMES_ICZN",
                          group(c, "higher", "family", "genus"), "GENUS", "SPECIES"),
    }
    # SpeciesNotBinominal and SubspeciesNotTrinominal are intended for
    # species-group names; the class method is defined as an instance
    # method there, so TaxonWorks falls back to all ICZN ranks.
    extra = {"species": ["Iczn::Unavailable::NonBinominal::SpeciesNotBinominal",
                         "Iczn::Unavailable::NonBinominal::SubspeciesNotTrinominal"]}
    for g, (const, ranks, good_rank, bad_rank) in groups.items():
        cs = classes(c, lambda x, k=const: x["applicable_ranks_constant"] == k)
        us = uris(cs) + by_class(*extra.get(g, []))
        us = sorted(set(us))
        what = {"family": "family-group", "genus": "genus-group", "species": "species-group",
                "above_species": "genus-group or higher"}[g]
        r = rule(f"tw_iczn_status_rank_{g}", "name", "col__rank_id", f"Status applies to {what} names only",
                 "regex", "soft", "warn",
                 [code_is(c), cond("col__status_id", "in", us), nonempty("col__rank_id")],
                 {"pattern": alt(ranks)},
                 f"This nomenclatural status applies only to {what} names.", 30, t)
        add(c, r, [(f"status on {bad_rank}", srow(c, us[0], bad_rank))],
            [(f"status on {good_rank}", srow(c, us[0], good_rank))])


def year_windows():
    """TaxonNameClassification#sv_proper_year: ICZN statuses carry a
    publication-year window (code_applicability_start/end_year)."""
    c = "iczn"
    t = f"TaxonNameClassification#sv_proper_year ({TNC}) with code_applicability_start_year/end_year"
    win = {}
    for x in classes(c):
        s, e = x["code_applicability_start_year"], x["code_applicability_end_year"]
        if s > e:
            # PreLinnean has the window 1758..1757; the intended rule is
            # a publication year before 1758.
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
            rid, rname = "tw_iczn_status_year_after_1757", "Status used on a name published after 1757"
            msg = "ICZN statuses apply to names published in 1758 or later."
        elif s is None:
            rid, rname = "tw_iczn_status_year_pre_linnean", "Pre-Linnean status on a name published before 1758"
            msg = "The pre-Linnean status applies only to names published before 1758."
        else:
            span = f"{s}–{e}" if e < 9999 else f"{s} or later"
            key = f"{s}_{e}" if e < 9999 else f"{s}_on"
            rid, rname = f"tw_iczn_status_year_{key}", f"Status limited to names published {span}"
            msg = f"This status applies only to names published {'in ' if e < 9999 else ''}{span}."
        r = rule(rid, "name", "col__published_in_year", rname, "range", "soft", "warn",
                 [code_is(c), cond("col__status_id", "in", us), cond("col__published_in_year", "matches", "^[0-9]{4}$")],
                 params, msg + " Check the year and the status.", 30, t)
        bad = str((s - 1) if s and s > 1 else (e + 1))
        good = str(s if s and s > 1 else e)
        add(c, r, [(f"year {bad}", srow(c, us[0], "SPECIES", bad))], [(f"year {good}", srow(c, us[0], "SPECIES", good))])


def not_specific(code):
    """TaxonNameClassification#sv_not_specific_classes."""
    skip = {P + "Iczn::Available::Valid", P + "Iczn::Available::Invalid", P + "Iczn::Available::Invalid::Homonym"}
    cs = [x for x in classes(code) if x["not_specific_defined_by"] == x["class"] and x["class"] not in skip]
    if not cs:
        return
    us = uris(cs)
    t = f"TaxonNameClassification#sv_not_specific_classes ({TNC}) and its subclass overrides"
    r = rule(f"tw_{code}_status_not_specific", "name", "col__status_id", "More specific status preferred",
             "absence", "soft", "info", [code_is(code), cond("col__status_id", "in", us)], {},
             "This status has more specific sub-statuses. Choose the one that states the reason, for example "
             "'nomen nudum: no description' rather than 'nomen nudum'.", 40, t)
    specific = [u for u in status_uris(code) if u not in us]
    add(code, r, [("general status", srow(code, us[0]))], [("specific status", srow(code, specific[0]))])


def prefer_relationship():
    us = by_class("Iczn::Available::Invalid", "Iczn::Available::Invalid::Homonym")
    r = rule("tw_iczn_status_prefer_relationship", "name", "col__status_id", "Relationship preferred over status",
             "absence", "soft", "info", [code_is("iczn"), cond("col__status_id", "in", us)], {},
             "This status can be used, but a name relationship (for example a synonym or homonym link) "
             "records the reason more precisely.", 40,
             "TaxonNameClassification::Iczn::Available::Invalid#sv_not_specific_classes "
             "(app/models/taxon_name_classification/iczn/available/invalid.rb)")
    add("iczn", r, [("invalid status", srow("iczn", us[0]))],
        [("nomen nudum status", srow("iczn", by_class("Iczn::Unavailable::NomenNudum")[0]))])


def roots_and_latinized():
    r = rule("tw_status_code_root", "name", "col__status_id", "Status is a specific nomenclatural status", "absence",
             "hard", "error", [cond("col__status_id", "in", CODE_ROOTS)], {},
             "The status is only the name of a nomenclatural code. Choose a specific status.", 15,
             f"TaxonNameClassification#validate_taxon_name_classification ({TNC})")
    add(None, r, [("code root as status", srow("iczn", CODE_ROOTS[0]))],
        [("no status", srow("iczn", ""))])
    lat = latinized()
    r = rule("tw_status_is_grammatical_term", "name", "col__status_id", "Status is not a grammatical term",
             "absence", "soft", "warn", [cond("col__status_id", "in", lat)], {},
             "The status holds a grammatical gender or part-of-speech term, which is not a nomenclatural status. "
             "Record gender in the gender field and clear the status.", 30,
             "TaxonNameClassification::Latinized (app/models/taxon_name_classification/latinized.rb)")
    add(None, r, [("gender term as status", srow("iczn", lat[0]))], [("no status", srow("iczn", ""))])


def gender_rank(code):
    """Latinized::Gender#validate_applicable_rank (hard) and
    Latinized::PartOfSpeech applicable_ranks (soft)."""
    cid = CODES[code]["id"]
    gg = group(code, "genus")
    r = rule(f"tw_{code}_gender_genus_group_only", "name", "col__gender_id", "Gender set only on genus-group names",
             "absence", "hard", "error",
             [code_is(code), nonempty("col__rank_id"), cond("col__rank_id", "not_in", gg), nonempty("col__gender_id")],
             {}, "Grammatical gender applies only to genus-group names.", 20,
             "TaxonNameClassification::Latinized::Gender#validate_applicable_rank "
             "(app/models/taxon_name_classification/latinized/gender.rb)")
    add(code, r,
        [("gender on species", [name("n1", "Aus bus", col__code_id=cid, col__rank_id="SPECIES", col__gender_id="FEMININE")])],
        [("gender on genus", [name("n1", "Aus", col__code_id=cid, col__rank_id="GENUS", col__gender_id="FEMININE")])])
    sg = group(code, "species")
    r = rule(f"tw_{code}_gender_agreement_species_group_only", "name", "col__gender_agreement",
             "Gender agreement set only on species-group names", "absence", "soft", "warn",
             [code_is(code), nonempty("col__rank_id"), cond("col__rank_id", "not_in", sg),
              cond("col__gender_agreement", "exists")],
             {}, "Gender agreement (the part of speech of an epithet) applies only to species-group names.", 30,
             "TaxonNameClassification#sv_proper_classification with Latinized::PartOfSpeech applicable_ranks "
             "(app/models/taxon_name_classification/latinized/part_of_speech.rb)")
    add(code, r,
        [("agreement on genus", [name("n1", "Aus", col__code_id=cid, col__rank_id="GENUS", col__gender_agreement=1)])],
        [("agreement on species", [name("n1", "Aus bus", col__code_id=cid, col__rank_id="SPECIES", col__gender_agreement=1)])])


def build():
    for c in CODES:
        code_mismatch(c)
        not_specific(c)
        gender_rank(c)
    rank_applicability()
    year_windows()
    prefer_relationship()
    roots_and_latinized()
