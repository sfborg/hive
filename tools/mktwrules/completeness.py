"""Completeness reminders (TaxonWorks soft validations in the
missing_relationships, missing_fields, missing_classifications,
missing_roles and primary_types sets)."""
from lib import *
from twdata import *

SVE = "app/models/protonym/soft_validation_extensions.rb"


def avail(code):
    u = unavailable(code)
    return [cond("col__status_id", "not_in", u)] if u else []


def missing_type(code):
    for g, rank, typ_rank, what, tw in [
        ("genus", "GENUS", "SPECIES", "type species", "sv_missing_type_species"),
        ("family", "FAMILY", "GENUS", "type genus", "sv_missing_type_genus"),
    ]:
        conds = [code_is(code), rank_in(group(code, g))] + avail(code)
        r = rule(f"tw_{code}_missing_{what.replace(' ', '_')}", "name", "col__id", f"Missing {what}",
                 "foreign_key_exists", "soft", "info", conds,
                 {"field": "col__id", "relation": "name:type-name"},
                 f"No {what} is recorded for this {g}-group name. Add a type name relation.", 50,
                 f"Protonym#{tw} ({SVE})")
        cid = CODES[code]["id"]
        base = [name("n1", "Aus", col__code_id=cid, col__rank_id=rank, col__uninomial="Aus")]
        add(code, r, [(f"{rank} without type", base)],
            [(f"{rank} with type", base + [name("n2", "Aus bus", col__code_id=cid, col__rank_id=typ_rank),
                                          rel("n1", "n2", "TYPE")])])


def missing_publication(code):
    ranks = group(code, "family", "genus", "species")
    t = "TaxonName#sv_missing_original_publication (app/models/taxon_name.rb)"
    cid = CODES[code]["id"]
    r = rule(f"tw_{code}_missing_original_publication", "name", "col__reference_id", "Missing original publication",
             "presence", "soft", "info", [code_is(code), rank_in(ranks)], {},
             "The original publication of this name is not recorded.", 50, t)
    add(code, r, [("no reference", [name("n1", "Aus", col__code_id=cid, col__rank_id="GENUS")])],
        [("reference set", [name("n1", "Aus", col__code_id=cid, col__rank_id="GENUS", col__reference_id="r1")])])
    r = rule(f"tw_{code}_missing_original_publication_page", "name", "col__published_in_page",
             "Missing page of original publication", "presence", "soft", "info",
             [code_is(code), rank_in(ranks), nonempty("col__reference_id")], {},
             "The page of the original publication is not recorded.", 50, t)
    add(code, r,
        [("reference without page", [name("n1", "Aus", col__code_id=cid, col__rank_id="GENUS", col__reference_id="r1")])],
        [("reference with page", [name("n1", "Aus", col__code_id=cid, col__rank_id="GENUS", col__reference_id="r1",
                                       col__published_in_page="12")])])


def missing_grammar(code):
    cid = CODES[code]["id"]
    r = rule(f"tw_{code}_missing_gender", "name", "col__gender_id", "Missing grammatical gender", "presence",
             "soft", "info", [code_is(code), rank_in(group(code, "genus"))] + avail(code), {},
             "The grammatical gender of this genus-group name is not recorded.", 50,
             f"Protonym#sv_missing_gender ({SVE})")
    add(code, r, [("genus without gender", [name("n1", "Aus", col__code_id=cid, col__rank_id="GENUS")])],
        [("genus with gender", [name("n1", "Aus", col__code_id=cid, col__rank_id="GENUS", col__gender_id="MASCULINE")])])
    r = rule(f"tw_{code}_missing_part_of_speech", "name", "col__gender_agreement", "Missing part of speech",
             "presence", "soft", "info", [code_is(code), rank_in(group(code, "species"))] + avail(code), {},
             "Gender agreement (whether the epithet is an adjective or participle that agrees with the genus, "
             "or a noun that does not) is not recorded.", 50,
             f"Protonym#sv_missing_part_of_speech ({SVE})")
    add(code, r, [("species without agreement", [name("n1", "Aus bus", col__code_id=cid, col__rank_id="SPECIES")])],
        [("species with agreement", [name("n1", "Aus bus", col__code_id=cid, col__rank_id="SPECIES",
                                          col__gender_agreement=0)])])
    # Flagged in TaxonWorks (off by default); debug severity keeps it out
    # of the default Issues view.
    r = rule(f"tw_{code}_missing_etymology", "name", "col__etymology", "Missing etymology", "presence",
             "soft", "debug", [code_is(code), rank_in(group(code, "genus", "species"))] + avail(code), {},
             "The etymology of this name is not recorded.", 60, f"Protonym#sv_missing_etymology ({SVE})")
    add(code, r, [("genus without etymology", [name("n1", "Aus", col__code_id=cid, col__rank_id="GENUS")])],
        [("genus with etymology", [name("n1", "Aus", col__code_id=cid, col__rank_id="GENUS",
                                        col__etymology="From Greek aus.")])])


def missing_roles(code):
    cid = CODES[code]["id"]
    ranks = group(code, "family", "genus", "species")
    t = f"Protonym#sv_missing_roles ({SVE})"
    r = rule(f"tw_{code}_missing_author_links", "name", "col__combination_authorship_id",
             "Missing links to name authors", "presence", "soft", "info",
             [code_is(code), rank_in(ranks), cond("col__authorship", "matches", "^[^(]")], {},
             "The authors of this name are not linked to person records.", 50, t)
    add(code, r, [("unlinked author", [name("n1", "Aus", col__code_id=cid, col__rank_id="GENUS", col__authorship="Smith, 1900")])],
        [("linked author", [name("n1", "Aus", col__code_id=cid, col__rank_id="GENUS", col__authorship="Smith, 1900",
                                 col__combination_authorship_id="p1")])])
    r = rule(f"tw_{code}_missing_original_author_links", "name", "col__basionym_authorship_id",
             "Missing links to original name authors", "presence", "soft", "info",
             [code_is(code), rank_in(ranks), cond("col__authorship", "matches", "^\\(")], {},
             "The authors of the original combination are not linked to person records.", 50, t)
    add(code, r, [("unlinked author", [name("n1", "Aus bus", col__code_id=cid, col__rank_id="SPECIES",
                                            col__authorship="(Smith, 1900)")])],
        [("linked author", [name("n1", "Aus bus", col__code_id=cid, col__rank_id="SPECIES",
                                 col__authorship="(Smith, 1900)", col__basionym_authorship_id="p1")])])


PRIMARY = ["HOLOTYPE", "LECTOTYPE", "NEOTYPE"]


def primary_types(code):
    cid = CODES[code]["id"]
    conds = [code_is(code), rank_in(group(code, "species"))] + avail(code)
    for s in PRIMARY + ["SYNTYPE"]:
        conds.append(cond("col__name_id", "not_exists", None, f"name:{s.lower()}"))
    r = rule(f"tw_{code}_missing_primary_type", "name", "col__id", "Missing primary type", "foreign_key_exists",
             "soft", "info", conds, {"field": "col__id", "relation": "name:holotype"},
             "No holotype, lectotype, neotype or syntype is recorded for this species-group name.", 50,
             f"Protonym#sv_primary_types ({SVE})")
    base = [name("n1", "Aus bus", col__code_id=cid, col__rank_id="SPECIES")]
    add(code, r, [("species without types", base), ("only a paratype", base + [tm("n1", "PARATYPE")])],
        [("holotype", base + [tm("n1", "HOLOTYPE")]), ("syntype", base + [tm("n1", "SYNTYPE")])])
    r = rule(f"tw_{code}_primary_type_repository", "type_material", "col__institution_code",
             "Primary type repository recorded", "presence", "soft", "warn",
             [cond("col__status_id", "in", PRIMARY + ["SYNTYPE"]),
              cond("col__code_id", "equals", cid, "type_material:name")], {},
             "The repository (institution code) of this primary type or syntype is not recorded.", 40,
             f"Protonym#sv_primary_types_repository ({SVE})")
    add(code, r, [("holotype without institution", base + [tm("n1", "HOLOTYPE")])],
        [("holotype with institution", base + [tm("n1", "HOLOTYPE", col__institution_code="NHMUK")]),
         ("paratype without institution", base + [tm("n1", "PARATYPE")])])


def build():
    for c in CODES:
        missing_type(c)
        missing_publication(c)
        missing_grammar(c)
        missing_roles(c)
    for c in ("iczn", "icn", "icnp"):
        primary_types(c)
