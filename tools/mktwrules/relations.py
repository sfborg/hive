"""Coordinated names, homonymy and synonymy, name-relation structure
and other name-level rules."""
from lib import *
from twdata import *
from placement import human

SVE = "app/models/protonym/soft_validation_extensions.rb"
TNRF = "app/models/taxon_name_relationship"
CODE_RELATIONS = ["LATER_HOMONYM", "REPLACEMENT_NAME", "HOMOTYPIC", "SPELLING_CORRECTION", "SUPERFLUOUS",
                  "CONSERVED", "BASEDON"]


def avail(code):
    u = unavailable(code)
    return [cond("col__status_id", "not_in", u)] if u else []


def coordinated():
    """Protonym#sv_validate_coordinated_names_* for a nominotypical
    subgenus and its genus, and a nominotypical subspecies and its
    species (the coordinated-name list is built for ICZN names only)."""
    c = "iczn"
    pairs = {
        "subgenus": ([cond("col__rank_id", "equals", "SUBGENUS"), nonempty("col__infrageneric_epithet"),
                      cond("col__infrageneric_epithet", "equals", {"field": "col__genus"}),
                      cond("col__rank_id", "equals", "GENUS", "name:parent-name")], "genus"),
        "subspecies": ([cond("col__rank_id", "equals", "SUBSPECIES"), nonempty("col__infraspecific_epithet"),
                        cond("col__infraspecific_epithet", "equals", {"field": "col__specific_epithet"}),
                        cond("col__rank_id", "equals", "SPECIES", "name:parent-name")], "species"),
    }

    def rows(kind, child_extra, parent_extra):
        if kind == "subgenus":
            p = dict(col__code_id="ZOOLOGICAL", col__rank_id="GENUS", col__uninomial="Aus")
            ch = dict(col__code_id="ZOOLOGICAL", col__rank_id="SUBGENUS", col__genus="Aus",
                      col__infrageneric_epithet="Aus")
            psci, csci = "Aus", "Aus (Aus)"
        else:
            p = dict(col__code_id="ZOOLOGICAL", col__rank_id="SPECIES", col__genus="Aus", col__specific_epithet="bus")
            ch = dict(col__code_id="ZOOLOGICAL", col__rank_id="SUBSPECIES", col__genus="Aus",
                      col__specific_epithet="bus", col__infraspecific_epithet="bus")
            psci, csci = "Aus bus", "Aus bus bus"
        p.update(parent_extra)
        ch.update(child_extra)
        return [name("p", psci, **p), taxon("tp", "p"), name("c", csci, **ch), taxon("tc", "c", "tp")]

    fields = [
        ("author", "col__authorship", "author", "sv_validate_coordinated_names_author", [], "Smith, 1900", "Jones, 1901", None),
        ("year", "col__published_in_year", "year of publication", "sv_validate_coordinated_names_year", [], "1900", "1901", None),
        ("source", "col__reference_id", "original publication", "sv_validate_coordinated_names_source", [], "r1", "r2", None),
        ("page", "col__published_in_page", "page of the original publication", "sv_validate_coordinated_names_page",
         [nonempty("col__reference_id"),
          cond("col__reference_id", "equals", {"field": "col__reference_id"}, "name:parent-name")], "12", "13",
         {"col__reference_id": "r1"}),
        ("etymology", "col__etymology", "etymology", "sv_validate_coordinated_names_etymology", avail(c),
         "From Latin aus.", "From Greek aus.", None),
        ("gender", "col__gender_id", "grammatical gender", "sv_validate_coordinated_names_gender", avail(c),
         "FEMININE", "MASCULINE", None),
        ("part_of_speech", "col__gender_agreement", "gender agreement (part of speech)",
         "sv_validate_coordinated_names_part_of_speech", avail(c), 1, 0, None),
    ]
    for kind, (pconds, parent) in pairs.items():
        for key, field, what, tw, extra, v1, v2, both in fields:
            if key == "gender" and kind != "subgenus":
                continue
            if key == "part_of_speech" and kind != "subspecies":
                continue
            conds = [code_is(c)] + pconds + extra
            r = rule(f"tw_iczn_coordinated_{kind}_{key}", "name", field,
                     f"Nominotypical {kind} matches its {parent}: {what}", "related_field_equals", "soft", "warn",
                     conds, {"relation": "name:parent-name", "field": field, "skip_if_either_empty": False},
                     f"The {what} of this nominotypical {kind} differs from that of its {parent}. "
                     "Coordinated names share author, year, publication and type.", 30, f"Protonym#{tw} ({SVE})")
            b = dict(both or {})
            add(c, r, [(f"{field} differs", rows(kind, {**b, field: v2}, {**b, field: v1}))],
                [(f"{field} equal", rows(kind, {**b, field: v1}, {**b, field: v1}))])


def homonyms():
    c = "iczn"
    tr = f"{TNRF}/iczn/invalidating/homonym.rb"
    base = [name("j", "Aus bus Smith, 1900", col__code_id="ZOOLOGICAL", col__rank_id="SPECIES"),
            name("s", "Aus bus Jones, 1850", col__code_id="ZOOLOGICAL", col__rank_id="SPECIES"),
            rel("j", "s", "LATER_HOMONYM")]
    repl = [name("n", "Aus cus Smith, 1920", col__code_id="ZOOLOGICAL", col__rank_id="SPECIES"),
            rel("n", "j", "REPLACEMENT_NAME")]
    synonymised = [taxon("t", "s"), syn("t", "j")]
    homonym = cond("col__id", "exists", None, "name:senior-homonym")
    not_syn = cond("col__name_id", "not_exists", None, "name:synonym-usage")
    r = rule("tw_iczn_homonym_missing_substitute", "name", "col__id", "Homonym has a substitute name",
             "foreign_key_exists", "soft", "warn",
             [code_is(c), homonym, not_syn, cond("col__id", "not_exists", None, "name:replaced-by")],
             {"field": "col__id", "relation": "name:replaced-by"},
             "This name is a later homonym, but no replacement name is recorded and it is not treated as a synonym. "
             "Add a replacement name or a synonym link.", 30,
             f"TaxonNameRelationship::Iczn::Invalidating::Homonym#sv_missing_nomen_novum ({tr})")
    add(c, r, [("homonym alone", base)], [("homonym with replacement", base + repl),
                                         ("homonym as synonym", base + synonymised)])
    r = rule("tw_iczn_homonym_replacement_not_synonym", "name", "col__id",
             "Replaced homonym is linked as a synonym", "foreign_key_exists", "soft", "warn",
             [code_is(c), homonym, cond("col__id", "exists", None, "name:replaced-by")],
             {"field": "col__id", "relation": "name:synonym-usage"},
             "This later homonym has a replacement name but is not linked as a synonym. Treat it as a synonym "
             "of the valid name.", 30,
             f"TaxonNameRelationship::Iczn::Invalidating::Homonym#sv_missing_replacement_name ({tr})")
    add(c, r, [("replaced, not synonym", base + repl)], [("replaced and synonym", base + repl + synonymised)])
    hs = by_class("Iczn::Available::Invalid::Homonym")
    r = rule("tw_iczn_homonym_status_missing_substitute", "name", "col__id",
             "Name with homonym status is linked as a synonym", "foreign_key_exists", "soft", "warn",
             [code_is(c), cond("col__status_id", "in", hs)],
             {"field": "col__id", "relation": "name:synonym-usage"},
             "This name has the status 'homonym', but no substitute name is linked. Treat it as a synonym of the "
             "substitute name.", 30, f"Protonym#sv_missing_substitute_name ({SVE})")
    one = [name("j", "Aus bus", col__code_id="ZOOLOGICAL", col__rank_id="SPECIES", col__status_id=hs[0])]
    add(c, r, [("homonym status only", one)],
        [("homonym status, synonym", one + [name("s", "Aus cus", col__code_id="ZOOLOGICAL"), taxon("t", "s"), syn("t", "j")])])


def family_invalid():
    c = "iczn"
    fam = group(c, "family")
    inv = descendants_and_self("Iczn::Available::Invalid")
    hs = by_class("Iczn::Available::Invalid::Homonym")
    t1 = f"Protonym#sv_family_is_invalid ({SVE})"

    def rows(status="", tg_status="", homonym=True):
        rs = [name("f", "Aidae", col__code_id="ZOOLOGICAL", col__rank_id="FAMILY", col__uninomial="Aidae",
                   col__status_id=status),
              name("g", "Aus", col__code_id="ZOOLOGICAL", col__rank_id="GENUS", col__uninomial="Aus",
                   col__status_id=tg_status),
              rel("f", "g", "TYPE")]
        if homonym:
            rs += [name("g0", "Aus", col__code_id="ZOOLOGICAL", col__rank_id="GENUS"), rel("g", "g0", "LATER_HOMONYM")]
        return rs
    r = rule("tw_iczn_family_invalid_type_genus_homonym", "name", "col__status_id",
             "Family with a homonym type genus is invalid", "regex", "soft", "warn",
             [code_is(c), rank_in(fam), cond("col__status_id", "not_in", unavailable(c)),
              cond("col__name_id", "exists", None, "name:type-name-later-homonym")],
             {"pattern": alt([u.replace(".", "\\.") for u in inv])},
             "The type genus of this family-group name is a later homonym, so the name is invalid. Record the "
             "status 'invalid: homonymy of type genus' (or another invalid status).", 30, t1)
    ht = by_class("Iczn::Available::Invalid::HomonymyOfTypeGenus")
    add(c, r, [("type genus homonym, no status", rows())], [("status set", rows(ht[0]))])
    r = rule("tw_iczn_family_invalid_type_genus_homonym_status", "name", "col__status_id",
             "Family whose type genus has homonym status is invalid", "regex", "soft", "warn",
             [code_is(c), rank_in(fam), cond("col__status_id", "not_in", unavailable(c)),
              cond("col__status_id", "in", hs, "name:type-name")],
             {"pattern": alt([u.replace(".", "\\.") for u in inv])},
             "The type genus of this family-group name has the status 'homonym', so the name is invalid. Record "
             "an invalid status for the family-group name.", 30, t1)
    add(c, r, [("type genus status homonym", rows("", hs[0], False))], [("status set", rows(ht[0], hs[0], False))])
    r = rule("tw_iczn_family_invalid_no_substitute", "name", "col__id", "Invalid family has a substitute name",
             "foreign_key_exists", "soft", "warn",
             [code_is(c), rank_in(fam), cond("col__status_id", "in", inv)],
             {"field": "col__id", "relation": "name:synonym-usage"},
             "This family-group name is marked invalid, but no substitute name is linked. Treat it as a synonym "
             "of the valid name.", 30, f"Protonym#sv_family_is_invalid_no_substitute ({SVE})")
    one = [name("f", "Aidae", col__code_id="ZOOLOGICAL", col__rank_id="FAMILY", col__status_id=ht[0])]
    add(c, r, [("invalid, not synonym", one)],
        [("invalid, synonym", one + [name("f2", "Bidae", col__code_id="ZOOLOGICAL"), taxon("t", "f2"), syn("t", "f")])])


def infrasubspecific():
    c = "iczn"
    targets = by_class("Iczn::Unavailable::VarietyOrFormAfter1960", "Iczn::Unavailable::Excluded::Infrasubspecific")
    r = rule("tw_iczn_missing_infrasubspecific_status", "name", "col__status_id",
             "Variety or form described after 1960 is infrasubspecific", "regex", "soft", "warn",
             [code_is(c), rank_in(["VARIETY", "SUBVARIETY", "FORM", "SUBFORM"]),
              cond("col__published_in_year", "greater_than", 1960),
              cond("col__status_id", "not_in", unavailable(c))],
             {"pattern": alt([u.replace(".", "\\.") for u in targets])},
             "Under the ICZN a name proposed as a variety or form after 1960 is infrasubspecific and unavailable, "
             "unless before 1985 it was adopted as valid or treated as a senior homonym. Record the status.", 30,
             f"Protonym#sv_missing_infrasubspecific_status ({SVE})")
    v = dict(col__code_id="ZOOLOGICAL", col__rank_id="VARIETY", col__published_in_year="1970")
    add(c, r, [("variety 1970", [name("n1", "Aus bus var. cus", **v)])],
        [("variety 1950", [name("n1", "Aus bus var. cus", **{**v, "col__published_in_year": "1950"})]),
         ("variety 1970 with status", [name("n1", "Aus bus var. cus", **{**v, "col__status_id": targets[0]})])])


def author_parentheses():
    t = "Protonym#verbatim_author_with_closed_parens_when_present (app/models/protonym.rb)"
    for c in ("iczn", "icvcn"):
        cid = CODES[c]["id"]
        r = rule(f"tw_{c}_authorship_parentheses", "name", "col__authorship", "Authorship parentheses are closed",
                 "regex", "hard", "error", [code_is(c), nonempty("col__authorship")],
                 {"pattern": "^(?:\\([^()]+\\)|[^()]+)$"},
                 "The authorship must either be wholly enclosed in one pair of parentheses or contain none.", 20, t)
        add(c, r, [("unclosed", [name("n1", "Aus bus", col__code_id=cid, col__authorship="(Smith, 1900")])],
            [("closed", [name("n1", "Aus bus", col__code_id=cid, col__authorship="(Smith, 1900)")]),
             ("none", [name("n1", "Aus bus", col__code_id=cid, col__authorship="Smith, 1900")])])


def relation_rules(code):
    """Rules on name_relation rows (TaxonNameRelationship hard validations)."""
    cid = CODES[code]["id"]
    subj = "name_relation:subject-name"
    t = f"TaxonNameRelationship#validate_subject_and_object_share_code ({TNRF}.rb)"
    r = rule(f"tw_{code}_relation_same_code", "name_relation", "col__related_name_id",
             "Related names share the nomenclatural code", "related_field_in_set", "hard", "error",
             [cond("col__code_id", "equals", cid, subj)],
             {"relation": "name_relation:related-name", "related_field": "col__code_id", "allowed_values": [cid],
              "skip_if_empty": True},
             "The two names in this relation are governed by different nomenclatural codes.", 15, t)
    other = next(CODES[o]["id"] for o in CODES if o != code)
    add(code, r,
        [("different codes", [name("a", "Aus", col__code_id=cid), name("b", "Bus", col__code_id=other),
                              rel("a", "b", "LATER_HOMONYM")])],
        [("same code", [name("a", "Aus", col__code_id=cid), name("b", "Bus", col__code_id=cid),
                        rel("a", "b", "LATER_HOMONYM")])])
    if code != "icvcn":
        # All ICVCN ranks share one rank group in TaxonWorks.
        t = f"TaxonNameRelationship#validate_subject_and_object_ranks ({TNRF}.rb)"
        for g in ("higher", "family", "genus", "species"):
            ranks = group(code, g)
            r = rule(f"tw_{code}_relation_rank_group_{g}", "name_relation", "col__related_name_id",
                     f"Related name is in the {g} group", "related_field_in_set", "hard", "error",
                     [cond("col__type_id", "in", CODE_RELATIONS), cond("col__code_id", "equals", cid, subj),
                      cond("col__rank_id", "in", ranks, subj)],
                     {"relation": "name_relation:related-name", "related_field": "col__rank_id",
                      "allowed_values": ranks, "skip_if_empty": True},
                     f"Both names in a homonym, synonym, replacement or spelling relation must be {g}-group names.",
                     15, t)
            other_rank = next(x for x in order(code) if x not in ranks)
            add(code, r,
                [("different groups", [name("a", "Aus", col__code_id=cid, col__rank_id=ranks[0]),
                                       name("b", "Bus", col__code_id=cid, col__rank_id=other_rank),
                                       rel("a", "b", "LATER_HOMONYM")])],
                [("same group", [name("a", "Aus", col__code_id=cid, col__rank_id=ranks[0]),
                                 name("b", "Bus", col__code_id=cid, col__rank_id=ranks[-1]),
                                 rel("a", "b", "LATER_HOMONYM")])])
    fam, gen, spe = group(code, "family"), group(code, "genus"), group(code, "species")
    typ = cond("col__type_id", "equals", "TYPE")
    tt = f"TaxonNameRelationship::Typification::Family/Genus#validate_subject_and_object_ranks ({TNRF}/typification/*.rb)"

    def trows(a_rank, b_rank):
        return [name("a", "Aus", col__code_id=cid, col__rank_id=a_rank), name("b", "Bus", col__code_id=cid,
                                                                               col__rank_id=b_rank),
                rel("a", "b", "TYPE")]
    r = rule(f"tw_{code}_type_designation_subject_rank", "name_relation", "col__name_id",
             "Typified name is a family- or genus-group name", "related_field_in_set", "hard", "error",
             [typ, cond("col__code_id", "equals", cid, subj)],
             {"relation": subj, "related_field": "col__rank_id", "allowed_values": fam + gen, "skip_if_empty": True},
             "Only family-group and genus-group names are typified by a name.", 15, tt)
    add(code, r, [("species typified", trows(spe[0], spe[-1]))], [("genus typified", trows(gen[0], spe[0]))])
    for g, ranks, obj, what in [("family", fam, gen, "genus-group"), ("genus", gen, spe, "species-group")]:
        r = rule(f"tw_{code}_type_designation_{g}_type_rank", "name_relation", "col__related_name_id",
                 f"Type of a {g}-group name is a {what} name", "related_field_in_set", "hard", "error",
                 [typ, cond("col__code_id", "equals", cid, subj), cond("col__rank_id", "in", ranks, subj)],
                 {"relation": "name_relation:related-name", "related_field": "col__rank_id", "allowed_values": obj,
                  "skip_if_empty": True},
                 f"The type of a {g}-group name must be a {what} name.", 15, tt)
        wrong = fam[0] if g == "genus" else spe[0]
        add(code, r, [(f"{g} typified by {wrong}", trows(ranks[0], wrong))],
            [(f"{g} typified by {obj[0]}", trows(ranks[0], obj[0]))])
    u = unavailable(code)
    if u:
        r = rule(f"tw_{code}_type_designation_available_name", "name_relation", "col__type_id",
                 "Typified name is available", "absence", "soft", "warn",
                 [typ, cond("col__status_id", "in", u, subj)], {},
                 "The typified name has an unavailable status, so it should not have a type designation.", 30,
                 f"TaxonNameRelationship::Typification#sv_specific_relationship ({TNRF}/typification.rb)")
        add(code, r,
            [("unavailable genus typified", [name("a", "Aus", col__code_id=cid, col__rank_id="GENUS", col__status_id=u[0]),
                                             name("b", "Aus bus", col__code_id=cid, col__rank_id="SPECIES"),
                                             rel("a", "b", "TYPE")])],
            [("genus typified", trows("GENUS", "SPECIES"))])


def structural():
    """Code-general rules from TaxonName, Protonym and
    TaxonNameRelationship hard validations."""
    tn = "TaxonName (app/models/taxon_name.rb)"
    r = rule("tw_published_in_year_integer", "name", "col__published_in_year", "Year of publication is an integer",
             "regex", "hard", "error", [nonempty("col__published_in_year")], {"pattern": "^[0-9]+$"},
             "The year of publication must be a whole number.", 15,
             "TaxonName validates :year_of_publication, date_year (app/models/taxon_name.rb)")
    add(None, r, [("1900a", [name("n1", "Aus", col__published_in_year="1900a")])],
        [("1900", [name("n1", "Aus", col__published_in_year="1900")])])
    r = rule("tw_published_in_year_range", "name", "col__published_in_year", "Year of publication is plausible",
             "range", "hard", "error", [cond("col__published_in_year", "matches", "^[0-9]+$")],
             {"min": 1000, "max": "current_year"},
             "The year of publication must be between 1000 and the current year.", 15,
             "TaxonName validates :year_of_publication, date_year (app/models/taxon_name.rb)")
    add(None, r, [("year 999", [name("n1", "Aus", col__published_in_year="999")]),
                  ("year 9999", [name("n1", "Aus", col__published_in_year="9999")])],
        [("year 1900", [name("n1", "Aus", col__published_in_year="1900")])])
    r = rule("tw_rank_required", "name", "col__rank_id", "Rank is recorded", "presence", "hard", "error", None, {},
             "Every name needs a rank.", 15, "Protonym validates_presence_of :rank_class (app/models/protonym.rb)")
    add(None, r, [("no rank", [name("n1", "Aus")])], [("rank", [name("n1", "Aus", col__rank_id="GENUS")])])
    for f in ("col__combination_authorship", "col__basionym_authorship"):
        r = rule(f"tw_{f[5:]}_without_digits", "name", f, "Author names contain no digits", "regex", "hard", "error",
                 [nonempty(f)], {"pattern": "^[^0-9]*$"},
                 "Author names must not contain digits; record the year in its own field.", 20,
                 "Protonym#verbatim_author_without_digits (app/models/protonym.rb)")
        add(None, r, [("with year", [name("n1", "Aus", **{f: "Smith 1900"})])],
            [("names only", [name("n1", "Aus", **{f: "Smith"})])])
    r = rule("tw_incomplete_combination", "name", "col__scientific_name", "Combination is complete", "absence",
             "soft", "warn", [cond("col__scientific_name", "matches", "(?:GENUS|SPECIES|VARIETY|FORM) NOT SPECIFIED")],
             {}, "A rank in this combination is not specified. Complete the combination.", 30,
             f"TaxonName#sv_incomplete_combination ({tn})")
    add(None, r, [("placeholder", [name("n1", "[GENUS NOT SPECIFIED] bus")])], [("complete", [name("n1", "Aus bus")])])
    for typ, rid, rname, msg, tw in [
        ("TYPE", "tw_type_designation_unique", "One type designation per name",
         "This name has more than one type designation, or the same one twice. Keep a single type name relation.",
         f"TaxonNameRelationship#validate_uniqueness_of_typification_object ({TNRF}.rb)"),
        ("BASIONYM", "tw_basionym_unique", "One original combination per name",
         "This name has more than one original combination (basionym) link, or the same link twice. Keep a single link.",
         f"TaxonNameRelationship validates_uniqueness_of :object_taxon_name_id for original combinations ({TNRF}.rb)"),
    ]:
        r = rule(rid, "name_relation", "col__name_id", rname, "count_across", "hard", "error",
                 [cond("col__type_id", "equals", typ)],
                 {"table": "name_relation", "match": [{"target": "col__name_id", "self": "col__name_id"},
                                                      {"target": "col__type_id", "self": "col__type_id"}],
                  "exclude_self": True, "expected": {"max": 0}}, msg, 15, tw)
        ns = [name("a", "Aus"), name("b", "Bus"), name("c", "Cus")]
        add(None, r, [("two", ns + [rel("a", "b", typ), rel("a", "c", typ)])], [("one", ns + [rel("a", "b", typ)])])


def build():
    coordinated()
    homonyms()
    family_invalid()
    infrasubspecific()
    author_parentheses()
    for c in CODES:
        relation_rules(c)
    structural()
