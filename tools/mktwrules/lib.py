"""Rule and fixture builders shared by the family modules."""
import itertools
from twdata import CODES

CORE = ("tw", "tw_iczn", "tw_icn", "tw_icnp", "tw_icvcn")
EXT = tuple(k.replace("tw", "tw_ext", 1) for k in CORE)
BUNDLES = {k: [] for k in CORE + EXT}
NEEDS = {k: set() for k in CORE + EXT}
SUPERSEDES = {k: [] for k in CORE + EXT}
FIXTURES = []
IDS = set()

_seq = itertools.count(1)


def uid(prefix):
    return f"{prefix}{next(_seq)}"


def bundle_for(code, ext=False):
    stem = "tw_ext" if ext else "tw"
    return stem if code is None else f"{stem}_{code}"


def rule(rid, table, field, name, vtype, enforcement, severity, conditions, params,
         message, priority, tw):
    if rid in IDS:
        raise SystemExit(f"duplicate rule id {rid}")
    IDS.add(rid)
    r = {
        "rule_id": rid,
        "table_name": table,
        "field_name": field,
        "rule_name": name,
        "description": "Ported from TaxonWorks " + tw + ".",
        "validator_type": vtype,
        "enforcement": enforcement,
        "severity": severity,
    }
    if conditions:
        r["conditions"] = conditions
    r["parameters"] = params or {}
    if severity == "error":
        r["error_message"] = message
        r["warning_message"] = ""
    else:
        r["error_message"] = ""
        r["warning_message"] = message
    r["is_active"] = True
    r["priority"] = priority
    return r


ID_COLUMNS = {"col__id", "col__name_id", "col__related_name_id", "col__parent_id", "col__taxon_id",
              "col__reference_id", "id", "name_id"}


def scoped(rows, tag):
    """Suffix every id in a case so cases can share one archive (mkdemo
    applies all fixtures to a single demo database)."""
    out = []
    for row in rows:
        v = {k: (f"{x}-{tag}" if k in ID_COLUMNS and isinstance(x, str) and x else x)
             for k, x in row["values"].items()}
        out.append({"table": row["table"], "values": v})
    return out


def add(code, r, bad, good, ext=False, needs=None, supersedes=None):
    key = bundle_for(code, ext)
    BUNDLES[key].append(r)
    NEEDS[key].update(needs or [])
    SUPERSEDES[key].extend(supersedes or [])
    rid = r["rule_id"]
    f = {
        "rule_id": rid,
        "description": r["rule_name"],
        "bad": [{"note": n, "rows": scoped(rows, f"{rid}-b{i}")} for i, (n, rows) in enumerate(bad)],
        "good": [{"note": n, "rows": scoped(rows, f"{rid}-g{i}")} for i, (n, rows) in enumerate(good)],
    }
    if ext:
        # The fixture archive needs the TaxonWorks round-trip columns
        # before it is opened, or the bundle is skipped.
        f["schema"] = "tw_ext"
    FIXTURES.append(f)


# ---- conditions -------------------------------------------------------

def cond(field, op, value=None, relation=None):
    c = {"field": field, "operator": op}
    if value is not None or op not in ("exists", "not_exists"):
        c["value"] = value
    if relation:
        c["relation"] = relation
    return c


def nonempty(field, relation=None):
    return cond(field, "not_equals", "", relation)


def code_is(code, relation=None):
    return cond("col__code_id", "equals", CODES[code]["id"], relation)


def rank_in(ranks, relation=None):
    return cond("col__rank_id", "in", list(ranks), relation)


def owning(field, op, value=None):
    return cond(field, op, value, "taxon:owning-name")


def alt(values):
    return "^(?:" + "|".join(values) + ")$"


# ---- fixture rows -----------------------------------------------------

def name(nid, sci, **kw):
    v = {"col__id": nid, "col__scientific_name": sci, "gn__scientific_name_string": sci}
    v.update(kw)
    return {"table": "name", "values": v}


def taxon(tid, nid, parent="", **kw):
    v = {"col__id": tid, "col__name_id": nid, "col__parent_id": parent}
    v.update(kw)
    return {"table": "taxon", "values": v}


def rel(a, b, typ, **kw):
    v = {"col__name_id": a, "col__related_name_id": b, "col__type_id": typ}
    v.update(kw)
    return {"table": "name_relation", "values": v}


def tm(nid, status, **kw):
    v = {"col__name_id": nid, "col__status_id": status}
    v.update(kw)
    return {"table": "type_material", "values": v}


def ref(rid, **kw):
    v = {"col__id": rid}
    v.update(kw)
    return {"table": "reference", "values": v}


def syn(tid, nid):
    return {"table": "synonym", "values": {"col__id": "", "col__taxon_id": tid, "col__name_id": nid}}


def placed(code, rank, parent_rank, child_extra=None, parent_extra=None):
    """A child taxon of `rank` under a parent taxon of `parent_rank`."""
    cid = CODES[code]["id"]
    pe = {"col__code_id": cid, "col__rank_id": parent_rank, "col__uninomial": "Parentia"}
    pe.update(parent_extra or {})
    ce = {"col__code_id": cid, "col__rank_id": rank, "col__uninomial": "Childia"}
    ce.update(child_extra or {})
    return [
        name("np", "Parentia", **pe),
        taxon("tp", "np"),
        name("nc", "Childia", **ce),
        taxon("tc", "nc", "tp"),
    ]
