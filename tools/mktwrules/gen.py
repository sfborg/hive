#!/usr/bin/env python3
"""Generate the TaxonWorks-derived gsvalidator bundles for hive.

Usage (from the repository root): python3 tools/mktwrules/gen.py

Writes pkg/tw_rules.json, pkg/tw_<code>_rules.json and
pkg/testdata/tw_rulefixtures.json, and adds the relations the rules use
to pkg/hive_rules.json when they are missing.
"""
import json
import os
import sys
from collections import OrderedDict

import lib
import names
import placement
import status
import completeness
import ext
import relations
import typerefs

RELATIONS = OrderedDict([
    ("name:parent-name", {
        "target_table": "name", "target_alias": "parent_name", "cardinality": "one",
        "join": [{"from": "name.col__id", "to": "taxon.col__name_id"},
                 {"from": "taxon.col__parent_id", "to": "taxon.col__id", "alias": "parent_taxon"},
                 {"from": "parent_taxon.col__name_id", "to": "name.col__id", "alias": "parent_name"}],
        "description": "From a name row, walk to its taxon, the parent taxon, and the parent's name."}),
    ("name:type-name", {
        "target_table": "name", "target_alias": "type_name", "cardinality": "one",
        "join": [{"from": "name.col__id", "to": "name_relation.col__name_id",
                  "where": [{"column": "col__type_id", "equals": "TYPE"}]},
                 {"from": "name_relation.col__related_name_id", "to": "name.col__id", "alias": "type_name"}],
        "description": "From a family- or genus-group name, walk through the TYPE name_relation to its type name."}),
    ("name:type-name-later-homonym", {
        "target_table": "name_relation", "target_alias": "type_homonymy", "cardinality": "one",
        "join": [{"from": "name.col__id", "to": "name_relation.col__name_id",
                  "where": [{"column": "col__type_id", "equals": "TYPE"}]},
                 {"from": "name_relation.col__related_name_id", "to": "name.col__id", "alias": "type_name"},
                 {"from": "type_name.col__id", "to": "name_relation.col__name_id", "alias": "type_homonymy",
                  "where": [{"column": "col__type_id", "equals": "LATER_HOMONYM"}]}],
        "description": "From a name, walk to its type name and then to a LATER_HOMONYM name_relation of that type name."}),
    ("name:senior-homonym", {
        "target_table": "name", "target_alias": "senior_homonym", "cardinality": "one",
        "join": [{"from": "name.col__id", "to": "name_relation.col__name_id",
                  "where": [{"column": "col__type_id", "equals": "LATER_HOMONYM"}]},
                 {"from": "name_relation.col__related_name_id", "to": "name.col__id", "alias": "senior_homonym"}],
        "description": "From a later homonym, walk through the LATER_HOMONYM name_relation to the senior homonym."}),
    ("name:replaced-by", {
        "target_table": "name", "target_alias": "replacement_name", "cardinality": "one",
        "join": [{"from": "name.col__id", "to": "name_relation.col__related_name_id",
                  "where": [{"column": "col__type_id", "equals": "REPLACEMENT_NAME"}]},
                 {"from": "name_relation.col__name_id", "to": "name.col__id", "alias": "replacement_name"}],
        "description": "From a replaced name, walk through the REPLACEMENT_NAME name_relation to the replacement name."}),
    ("name:synonym-usage", {
        "target_table": "synonym", "cardinality": "one",
        "join": [{"from": "name.col__id", "to": "synonym.col__name_id"}],
        "description": "From a name row, walk to a synonym row that uses it."}),
])
for status_id in ("HOLOTYPE", "LECTOTYPE", "NEOTYPE", "SYNTYPE"):
    RELATIONS[f"name:{status_id.lower()}"] = {
        "target_table": "type_material", "cardinality": "one",
        "join": [{"from": "name.col__id", "to": "type_material.col__name_id",
                  "where": [{"column": "col__status_id", "equals": status_id}]}],
        "description": f"From a name row, walk to one of its type_material rows with status {status_id}."}
NR_TYPE = "tw__name_relationship_type"
OC = "TaxonNameRelationship::OriginalCombination::"
CB = "TaxonNameRelationship::Combination::"
TYPIF = "TaxonNameRelationship::Typification::"
NOMEN = "http://purl.obolibrary.org/obo/"


def _element(cls, incoming=True):
    """A name_relation naming this name as an element of a combination
    (the element is the subject, the combination the object)."""
    col = "col__related_name_id" if incoming else "col__name_id"
    return {"target_table": "name_relation", "cardinality": "one",
            "join": [{"from": "name.col__id", "to": f"name_relation.{col}",
                      "where": [{"column": NR_TYPE, "equals": cls}]}],
            "description": f"From a name row, walk to a name_relation of TaxonWorks type {cls}."}


for _e in ("OriginalGenus", "OriginalSubgenus", "OriginalSpecies", "OriginalSubspecies", "OriginalVariety",
           "OriginalSubvariety", "OriginalForm", "OriginalSubform"):
    RELATIONS[f"name:element-{_e.lower()}"] = _element(OC + _e)
for _e in ("Genus", "Subgenus", "Section", "Subsection", "Series", "Subseries", "Species", "Subspecies",
           "Variety", "Subvariety", "Form", "Subform"):
    RELATIONS[f"name:element-{_e.lower()}"] = _element(CB + _e)
for _n, _cls in (("forgotten-relation", NOMEN + "NOMEN_0000106"),
                 ("secondary1961-relation", NOMEN + "NOMEN_0000292"),
                 ("family-before-1961-relation", NOMEN + "NOMEN_0000042"),
                 ("replacement-for", NOMEN + "NOMEN_0000270"),
                 ("unnecessary-replacement", NOMEN + "NOMEN_0000279")):
    RELATIONS[f"name:{_n}"] = _element(_cls, incoming=False)
RELATIONS["name:replaced-homonym-incoming"] = _element(NOMEN + "NOMEN_0000112")
for _n, _cls in (("subsequent-designation-of", TYPIF + "Genus::Subsequent::SubsequentDesignation"),
                 ("subsequent-monotypy-of", TYPIF + "Genus::Subsequent::SubsequentMonotypy")):
    RELATIONS[f"name:{_n}"] = _element(_cls)
RELATIONS["name:homonym-of-suppressed"] = {
    "target_table": "name_relation", "target_alias": "suppression", "cardinality": "one",
    "join": [{"from": "name.col__id", "to": "name_relation.col__name_id",
              "where": [{"column": NR_TYPE, "equals": NOMEN + "NOMEN_0000289"}]},
             {"from": "name_relation.col__related_name_id", "to": "name.col__id", "alias": "senior"},
             {"from": "senior.col__id", "to": "name_relation.col__name_id", "alias": "suppression",
              "where": [{"column": NR_TYPE, "equals": NOMEN + "NOMEN_0000283"}]}],
    "description": "From a later homonym, walk to the senior homonym and on to a total-suppression "
                   "relationship of that senior name."}
RELATIONS["name:otu"] = {
    "target_table": "tw__otu", "cardinality": "one",
    "join": [{"from": "name.col__id", "to": "tw__otu.name_id"}],
    "description": "From a name row, walk to a TaxonWorks OTU of that name."}
RELATIONS["type_material:name"] = {
    "target_table": "name", "cardinality": "one",
    "join": [{"from": "type_material.col__name_id", "to": "name.col__id"}],
    "description": "From a type_material row, walk to the name it is the type of."}

META = {
    "tw": ("tw_sfga", "TaxonWorks-derived validation rules that apply to names of every nomenclatural code, "
                      "to name relations, type material and references."),
    "tw_iczn": ("tw_iczn_sfga", "TaxonWorks-derived validation rules for names governed by the ICZN "
                                "(col__code_id ZOOLOGICAL)."),
    "tw_icn": ("tw_icn_sfga", "TaxonWorks-derived validation rules for names governed by the ICN "
                              "(col__code_id BOTANICAL)."),
    "tw_icnp": ("tw_icnp_sfga", "TaxonWorks-derived validation rules for names governed by the ICNP "
                                "(col__code_id BACTERIAL)."),
    "tw_icvcn": ("tw_icvcn_sfga", "TaxonWorks-derived validation rules for names governed by the ICVCN "
                                  "(col__code_id VIRUS)."),
}
EXT_NOTE = (" These rules need the TaxonWorks round-trip columns, which are a proposal against the sfga "
            "schema and not yet adopted: the bundle's requires block names them, and hive skips the bundle "
            "and reports the missing columns when an archive does not have them.")
for _k, (_n, _d) in list(META.items()):
    META[_k.replace("tw", "tw_ext", 1)] = (_n.replace("tw_", "tw_ext_", 1), _d + EXT_NOTE)

COMMON = (" Each rule's description names the TaxonWorks class and method it is ported from. Statuses are "
          "matched as NOMEN ontology URIs in name.col__status_id. Relations are declared in hive_rules.json. "
          "Schema compatibility: SFGA v0.3.x.")


def used_relations(rules):
    out = set()
    for r in rules:
        for c in r.get("conditions", []):
            if c.get("relation"):
                out.add(c["relation"])
        p = r.get("parameters", {})
        if isinstance(p.get("relation"), str):
            out.add(p["relation"])
    return sorted(out)


def compact(obj, level=0):
    """JSON with two-space indentation; scalar lists and short objects
    stay on one line."""
    pad, inner = "  " * level, "  " * (level + 1)
    flat = json.dumps(obj, ensure_ascii=False)
    if not isinstance(obj, (dict, list)):
        return flat
    if isinstance(obj, list) and all(not isinstance(v, (dict, list)) for v in obj):
        return flat
    if len(flat) + len(inner) <= 110:
        return flat
    if isinstance(obj, list):
        return "[\n" + ",\n".join(inner + compact(v, level + 1) for v in obj) + "\n" + pad + "]"
    return "{\n" + ",\n".join(inner + json.dumps(k) + ": " + compact(v, level + 1) for k, v in obj.items()) + \
        "\n" + pad + "}"


def requires_for(key, rules):
    """Tables, columns and relations a bundle needs before its rules run."""
    tables, columns = [], {}
    for need in sorted(lib.NEEDS[key]):
        if "." in need:
            columns[need] = "text"
        else:
            tables.append(need)
    return {"tables": tables, "columns": columns, "relations": used_relations(rules)}


def write_json(path, obj):
    with open(path, "w") as f:
        f.write(compact(obj))
        f.write("\n")


def main():
    hive = sys.argv[1] if len(sys.argv) > 1 else "."
    for m in (names, placement, status, completeness, relations, typerefs, ext):
        m.build()
    pkg = os.path.join(hive, "pkg")
    for key, rules in lib.BUNDLES.items():
        rules.sort(key=lambda r: (r["priority"], r["rule_id"]))
        bname, desc = META[key]
        bundle = OrderedDict([
            ("name", bname), ("version", "0.2.0"), ("domain", "biodiversity"), ("author", "sfborg/hive"),
            ("description", desc + COMMON), ("created_at", "2026-09-22T00:00:00Z"), ("tables", []),
            ("requires", requires_for(key, rules)),
            ("relations", {}),
        ])
        if lib.SUPERSEDES[key]:
            bundle["supersedes"] = sorted(set(lib.SUPERSEDES[key]))
        bundle["rules"] = rules
        fname = "tw_rules.json" if key == "tw" else f"{key}_rules.json"
        write_json(os.path.join(pkg, fname), bundle)
        print(f"{fname}: {len(rules)} rules")
    os.makedirs(os.path.join(pkg, "testdata"), exist_ok=True)
    write_json(os.path.join(pkg, "testdata", "tw_rulefixtures.json"), lib.FIXTURES)
    print(f"tw_rulefixtures.json: {len(lib.FIXTURES)} fixtures")
    merge_relations(os.path.join(pkg, "hive_rules.json"))


def merge_relations(path):
    """Append missing relations to the relations block of hive_rules.json,
    leaving the rest of the file byte-for-byte unchanged."""
    s = open(path).read()
    existing = json.loads(s)["relations"]
    missing = OrderedDict()
    for k, v in RELATIONS.items():
        if k not in existing:
            missing[k] = v
        elif existing[k] != v:
            raise SystemExit(f"{path}: relation {k} differs from the generator's definition")
    if not missing:
        return
    end = s.index('\n  "rules": [')
    close = s.rindex("\n  }", 0, end)
    chunks = []
    for k, v in missing.items():
        body = json.dumps(v, indent=2).replace("\n", "\n    ")
        chunks.append(f"    {json.dumps(k)}: {body}")
    s = s[:close] + ",\n" + ",\n".join(chunks) + s[close:]
    json.loads(s)
    with open(path, "w") as f:
        f.write(s)
    print(f"hive_rules.json: added {len(missing)} relations")


if __name__ == "__main__":
    main()
