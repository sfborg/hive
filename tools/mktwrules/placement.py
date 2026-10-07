"""Classification placement rules: parent rank, rank order, priority
within a rank group, nominotypical placement, parent validity."""
from lib import *
from twdata import *

SVE = "app/models/protonym/soft_validation_extensions.rb"
TNR = "app/models/taxon_name.rb"


def human(rank):
    return rank.lower().replace("_", " ")


def listing(ranks):
    shown = [human(r) for r in ranks if r not in ("SECTION_BOTANY", "SUBSECTION_BOTANY", "SPECIES_AGGREGATE")]
    if len(shown) <= 6:
        return ", ".join(shown)
    return ", ".join(shown[:6]) + ", …"


def parent_rank(code):
    """Protonym#sv_validate_parent_rank against NomenclaturalRank valid_parents."""
    t = f"Protonym#sv_validate_parent_rank ({SVE}) with NomenclaturalRank valid_parents"
    ov = PARENT_OVERRIDES[code]
    for g in ("higher", "family", "genus", "species"):
        allowed = group(code, *PARENT_DEFAULTS[g])
        kids = [r for r in RANKS[code][g] if r not in ov]
        units = []
        if kids:
            units.append((f"tw_{code}_parent_rank_{g}_group", f"Parent rank of {g}-group names",
                          expand(kids), allowed))
        for r in RANKS[code][g]:
            if r in ov:
                units.append((f"tw_{code}_parent_rank_{r.lower()}", f"Parent rank of {human(r)} names",
                              expand([r]), expand(ov[r])))
        for rid, rname, children, parents in units:
            conds = [code_is(code), rank_in(children)]
            params = {"relation": "name:parent-name", "related_field": "col__rank_id",
                      "allowed_values": parents, "skip_if_empty": True}
            what = human(children[0]) if len(children) == 1 else f"{g}-group"
            msg = (f"A {what} name should be placed under a taxon of rank: {listing(parents)}. "
                   "If the placement is uncertain, record it as incertae sedis.")
            r = rule(rid, "name", "col__rank_id", rname, "related_field_in_set", "soft", "warn",
                     conds, params, msg, 30, t)
            bad_parent = next(x for x in order(code) if x not in parents and x not in children)
            add(code, r,
                [(f"{children[0]} under {bad_parent}", placed(code, children[0], bad_parent))],
                [(f"{children[0]} under {parents[0]}", placed(code, children[0], parents[0]))])


def rank_order(code):
    """Protonym#validate_parent_rank_is_higher (hard)."""
    t = "Protonym#validate_parent_rank_is_higher (app/models/protonym.rb)"
    params = {"parent_column": "col__parent_id", "rank_column": "col__rank_id", "code_column": "col__code_id",
              "join": {"from": "col__name_id", "to": "name.col__id"},
              "rank_order": {CODES[code]["id"]: order(code)}}
    r = rule(f"tw_{code}_parent_rank_higher", "taxon", "col__parent_id", "Parent rank is higher than the rank",
             "parent_rank_higher", "hard", "error", None, params,
             "The parent's rank must be higher than this taxon's rank.", 15, t)
    o = order(code)
    low, high = o[-1], o[0]
    add(code, r, [(f"{high} under {low}", placed(code, high, low))],
        [(f"{low} under {high}", placed(code, low, high))])


def priority(code):
    """Protonym#sv_parent_priority: within a rank group the parent should
    not be younger than the child."""
    t = f"Protonym#sv_parent_priority ({SVE})"
    for g in ("family", "genus", "species"):
        ranks = group(code, g)
        conds = [owning("col__code_id", "equals", CODES[code]["id"]), owning("col__rank_id", "in", ranks)]
        params = {
            "parent_column": "col__parent_id",
            "stop_when": {"join": {"from": "col__name_id", "to": "name.col__id"}, "field": "col__rank_id",
                          "operator": "in", "value": ranks},
            "assert": {"self_join": {"from": "col__name_id", "to": "name.col__id"},
                       "self_field": "col__published_in_year",
                       "ancestor_join": {"from": "col__name_id", "to": "name.col__id"},
                       "ancestor_field": "col__published_in_year", "operator": "greater_than_or_equals"},
        }
        r = rule(f"tw_{code}_parent_priority_{g}_group", "taxon", "col__parent_id",
                 f"Name not older than its {g}-group parent", "ancestor_field_check", "soft", "warn", conds, params,
                 f"This {g}-group name is older than its {g}-group parent. Within a rank group the parent "
                 "should be the oldest name; check the years and the placement.", 30, t)
        child, parent = {"family": ("SUBFAMILY", "FAMILY"), "genus": ("SUBGENUS", "GENUS"),
                         "species": ("SUBSPECIES", "SPECIES")}[g]
        add(code, r,
            [("child 1800, parent 1850", placed(code, child, parent, {"col__published_in_year": "1800"},
                                                 {"col__published_in_year": "1850"}))],
            [("child 1900, parent 1850", placed(code, child, parent, {"col__published_in_year": "1900"},
                                                 {"col__published_in_year": "1850"}))])


def nominotypical():
    """Protonym#check_new_parent_class, as a state check: a nominotypical
    subgenus or subspecies stays in its nominal genus or species."""
    t = "TaxonName#check_new_parent_class (app/models/taxon_name.rb)"
    c = "iczn"
    conds = [code_is(c), cond("col__rank_id", "equals", "SUBGENUS"), nonempty("col__infrageneric_epithet"),
             cond("col__infrageneric_epithet", "equals", {"field": "col__genus"})]
    r = rule("tw_iczn_nominotypical_subgenus_placement", "name", "col__genus",
             "Nominotypical subgenus is placed in its nominal genus", "related_field_equals", "hard", "error", conds,
             {"relation": "name:parent-name", "field": "col__genus", "related_field": "col__uninomial",
              "skip_if_either_empty": True},
             "A nominotypical subgenus must be placed in the genus of the same name.", 20, t)

    def sub(parent_uni):
        return [name("g", parent_uni, col__code_id="ZOOLOGICAL", col__rank_id="GENUS", col__uninomial=parent_uni),
                taxon("tg", "g"),
                name("s", "Aus (Aus)", col__code_id="ZOOLOGICAL", col__rank_id="SUBGENUS", col__genus="Aus",
                     col__infrageneric_epithet="Aus"),
                taxon("ts", "s", "tg")]
    add(c, r, [("Aus (Aus) under Bus", sub("Bus"))], [("Aus (Aus) under Aus", sub("Aus"))])

    conds = [code_is(c), cond("col__rank_id", "equals", "SUBSPECIES"), nonempty("col__infraspecific_epithet"),
             cond("col__infraspecific_epithet", "equals", {"field": "col__specific_epithet"})]
    r = rule("tw_iczn_nominotypical_subspecies_placement", "name", "col__specific_epithet",
             "Nominotypical subspecies is placed in its nominal species", "related_field_equals", "hard", "error",
             conds, {"relation": "name:parent-name", "field": "col__specific_epithet",
                     "related_field": "col__specific_epithet", "skip_if_either_empty": True},
             "A nominotypical subspecies must be placed in the species of the same name.", 20, t)

    def ssp(parent_ep):
        return [name("sp", f"Aus {parent_ep}", col__code_id="ZOOLOGICAL", col__rank_id="SPECIES", col__genus="Aus",
                     col__specific_epithet=parent_ep),
                taxon("tsp", "sp"),
                name("ssp", "Aus bus bus", col__code_id="ZOOLOGICAL", col__rank_id="SUBSPECIES", col__genus="Aus",
                     col__specific_epithet="bus", col__infraspecific_epithet="bus"),
                taxon("tssp", "ssp", "tsp")]
    add(c, r, [("Aus bus bus under Aus cus", ssp("cus"))], [("Aus bus bus under Aus bus", ssp("bus"))])


def parent_valid(code):
    """TaxonName#sv_parent_is_valid_name and #sv_conflicting_subordinate_taxa."""
    bad_status = unavailable_or_invalid(code)
    if not bad_status:
        return
    s = bad_status[0]
    cid = CODES[code]["id"]
    r = rule(f"tw_{code}_parent_is_valid_name", "taxon", "col__parent_id", "Parent is a valid name", "absence",
             "soft", "warn", [cond("col__status_id", "in", bad_status, "taxon:parent-owning-name")], {},
             "The parent's name has an unavailable or invalid nomenclatural status. Move this taxon to the "
             "parent's valid name.", 30, f"TaxonName#sv_parent_is_valid_name ({TNR})")
    add(code, r,
        [("parent status unavailable", placed(code, "SPECIES", "GENUS", None, {"col__status_id": s}))],
        [("parent status empty", placed(code, "SPECIES", "GENUS"))])
    r = rule(f"tw_{code}_conflicting_subordinate_taxa", "taxon", "col__id",
             "Unavailable or invalid name has no subordinate taxa", "count_across", "soft", "warn",
             [owning("col__status_id", "in", bad_status)],
             {"table": "taxon", "match": [{"target": "col__parent_id", "self": "col__id"}], "expected": {"max": 0}},
             "This taxon's name has an unavailable or invalid status, but other taxa are placed under it. "
             "Move the children to a valid taxon or correct the status.", 30,
             f"TaxonName#sv_conflicting_subordinate_taxa ({TNR})")
    # The count runs on the parent (tp), whose status is set here.
    add(code, r,
        [("status unavailable with a child", placed(code, "SPECIES", "GENUS", None, {"col__status_id": s}))],
        [("status unavailable without children",
          [name("np", "Parentia", col__code_id=cid, col__rank_id="GENUS", col__status_id=s), taxon("tp", "np")])])


def same_code():
    """Protonym#validate_same_nomenclatural_code."""
    r = rule("tw_parent_same_code", "name", "col__code_id", "Parent has the same nomenclatural code",
             "related_field_equals", "hard", "error", None,
             {"relation": "name:parent-name", "field": "col__code_id", "skip_if_either_empty": True},
             "This name and its parent are governed by different nomenclatural codes.", 15,
             "Protonym#validate_same_nomenclatural_code (app/models/protonym.rb)")
    add(None, r,
        [("ICN species under ICZN genus",
          placed("iczn", "SPECIES", "GENUS", {"col__code_id": "BOTANICAL"}))],
        [("both ICZN", placed("iczn", "SPECIES", "GENUS"))])


def extant_children():
    """Protonym#sv_extant_children, reported on the child."""
    r = rule("tw_extinct_parent_with_extant_child", "taxon", "col__extinct", "Extant taxon under an extinct parent",
             "related_field_equals", "soft", "warn",
             [cond("col__extinct", "not_equals", 1), cond("col__extinct", "equals", 1, "taxon:parent-taxon")],
             {"relation": "taxon:parent-taxon", "field": "col__extinct", "skip_if_either_empty": False},
             "This taxon is not marked extinct, but its parent is. Mark this taxon extinct or correct the parent.",
             30, f"Protonym#sv_extant_children ({SVE})")

    def rows(child):
        v = {} if child is None else {"col__extinct": child}
        return [name("np", "Parentia", col__rank_id="GENUS"), taxon("tp", "np", col__extinct=1),
                name("nc", "Parentia bus", col__rank_id="SPECIES"), taxon("tc", "nc", "tp", **v)]
    add(None, r, [("extant child", rows(0)), ("child extinct unknown", rows(None))],
        [("extinct child", rows(1))])


def duplicate_taxon():
    """Otu#sv_duplicate_otu."""
    r = rule("tw_duplicate_taxon", "taxon", "col__name_id", "Duplicate taxon for a name", "count_across", "soft",
             "info", None,
             {"table": "taxon", "match": [{"target": "col__name_id", "self": "col__name_id"},
                                          {"target": "col__name_phrase", "self": "col__name_phrase"}],
              "exclude_self": True, "expected": {"max": 0}, "skip_if_any_self_empty": False},
             "Another taxon uses the same name and name phrase. Merge the duplicates or distinguish them "
             "with a name phrase.", 40, "Otu#sv_duplicate_otu (app/models/otu.rb)")
    add(None, r,
        [("two taxa, same name", [name("n1", "Aus bus"), taxon("t1", "n1"), taxon("t2", "n1")])],
        [("two taxa, different phrase", [name("n1", "Aus bus"), taxon("t1", "n1"),
                                         taxon("t2", "n1", col__name_phrase="sensu lato")])])


def build():
    for c in ("iczn", "icn", "icnp"):
        parent_rank(c)
        priority(c)
    for c in ("iczn", "icn", "icnp", "icvcn"):
        rank_order(c)
        parent_valid(c)
    nominotypical()
    same_code()
    extant_children()
    duplicate_taxon()
