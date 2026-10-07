"""TypeMaterial and Source::Bibtex rules."""
from lib import *
from twdata import *

TM = "app/models/type_material.rb"
BIB = "app/models/source/bibtex/soft_validation_extensions.rb"

LEGAL_TYPES = {
    "iczn": ["HOLOTYPE", "PARATYPE", "PARALECTOTYPE", "NEOTYPE", "LECTOTYPE", "SYNTYPE"],
    "icn": ["HOLOTYPE", "PARATYPE", "LECTOTYPE", "NEOTYPE", "EPITYPE", "ISOTYPE", "SYNTYPE", "ISOSYNTYPE"],
}
PRIMARY = ["HOLOTYPE", "LECTOTYPE", "NEOTYPE"]


def species(cid, **kw):
    v = {"col__code_id": cid, "col__rank_id": "SPECIES"}
    v.update(kw)
    return name("n1", "Aus bus", **v)


def type_material(code):
    cid = CODES[code]["id"]
    tmn = "type_material:name"
    if code in LEGAL_TYPES:
        r = rule(f"tw_{code}_type_material_status", "type_material", "col__status_id",
                 f"Type status is used by the {CODES[code]['label']}", "regex", "hard", "error",
                 [nonempty("col__status_id"), cond("col__code_id", "equals", cid, tmn)],
                 {"pattern": alt(LEGAL_TYPES[code])},
                 f"This type status is not used under the {CODES[code]['label']}.", 15,
                 f"TypeMaterial#check_type_type ({TM})")
        bad = "EPITYPE" if code == "iczn" else "PARALECTOTYPE"
        add(code, r, [(bad, [species(cid), tm("n1", bad)])], [("HOLOTYPE", [species(cid), tm("n1", "HOLOTYPE")])])
    r = rule(f"tw_{code}_type_material_species_group", "type_material", "col__name_id",
             "Type material belongs to a species-group name", "related_field_in_set", "hard", "error",
             [cond("col__code_id", "equals", cid, tmn)],
             {"relation": tmn, "related_field": "col__rank_id", "allowed_values": group(code, "species"),
              "skip_if_empty": True},
             "Type specimens can be designated only for species-group names.", 15,
             f"TypeMaterial#check_protonym_rank ({TM})")
    add(code, r, [("genus", [name("n1", "Aus", col__code_id=cid, col__rank_id="GENUS"), tm("n1", "HOLOTYPE")])],
        [("species", [species(cid), tm("n1", "HOLOTYPE")])])


def type_material_general():
    # Two primary types, or primary types with syntypes, on one name.
    pairs = [("holotype", "lectotype"), ("holotype", "neotype"), ("lectotype", "neotype"),
             ("holotype", "syntype"), ("lectotype", "syntype"), ("neotype", "syntype")]
    for a, b in pairs:
        syntypes = b == "syntype"
        rid = f"tw_{a}_with_{b}"
        rname = f"{a.capitalize()} and {b} on the same name"
        msg = (f"This name has both a {a} and syntypes. Syntypes conflict with a primary type."
               if syntypes else f"This name has both a {a} and a {b}. A name has only one primary type.")
        tw = (f"TypeMaterial#sv_single_primary_type ({TM}) and Protonym#sv_primary_types "
              "(app/models/protonym/soft_validation_extensions.rb)")
        r = rule(rid, "name", "col__id", rname, "absence", "soft", "warn",
                 [cond("col__name_id", "exists", None, f"name:{a}"), cond("col__name_id", "exists", None, f"name:{b}")],
                 {}, msg, 30, tw)
        n = name("n1", "Aus bus", col__rank_id="SPECIES")
        add(None, r, [("both", [n, tm("n1", a.upper()), tm("n1", b.upper())])], [("one", [n, tm("n1", a.upper())])])
    r = rule("tw_duplicate_primary_type", "type_material", "col__status_id", "One primary type of each kind",
             "count_across", "soft", "warn", [cond("col__status_id", "in", PRIMARY)],
             {"table": "type_material", "match": [{"target": "col__name_id", "self": "col__name_id"},
                                                  {"target": "col__status_id", "self": "col__status_id"}],
              "exclude_self": True, "expected": {"max": 0}},
             "Another type-material record of this name has the same primary type status. A name has only one "
             "holotype, lectotype or neotype.", 30, f"TypeMaterial#sv_single_primary_type ({TM})")
    n = name("n1", "Aus bus", col__rank_id="SPECIES")
    add(None, r, [("two holotypes", [n, tm("n1", "HOLOTYPE"), tm("n1", "HOLOTYPE")])],
        [("holotype and paratypes", [n, tm("n1", "HOLOTYPE"), tm("n1", "PARATYPE"), tm("n1", "PARATYPE")])])
    r = rule("tw_type_material_unique", "type_material", "col__status_id", "Type-material record is unique",
             "count_across", "hard", "error", None,
             {"table": "type_material", "match": [{"target": "col__name_id", "self": "col__name_id"},
                                                  {"target": "col__status_id", "self": "col__status_id"},
                                                  {"target": "col__institution_code", "self": "col__institution_code"},
                                                  {"target": "col__catalog_number", "self": "col__catalog_number"}],
              "exclude_self": True, "expected": {"max": 0}},
             "The same specimen is recorded twice with the same type status for this name.", 15,
             f"TypeMaterial validates_uniqueness_of :type_type ({TM})")
    spec = dict(col__institution_code="NHMUK", col__catalog_number="123")
    add(None, r, [("same specimen twice", [n, tm("n1", "PARATYPE", **spec), tm("n1", "PARATYPE", **spec)])],
        [("two specimens", [n, tm("n1", "PARATYPE", **spec),
                            tm("n1", "PARATYPE", col__institution_code="NHMUK", col__catalog_number="124")])])
    tmn = "type_material:name"
    t = f"TypeMaterial#sv_type_source ({TM})"
    r = rule("tw_type_material_source", "type_material", "col__reference_id", "Type or name has a source",
             "presence", "soft", "warn", [cond("col__reference_id", "equals", "", tmn)], {},
             "Neither this type-material record nor its name has a reference.", 30, t)
    add(None, r, [("no references", [n, tm("n1", "HOLOTYPE")])],
        [("type reference", [n, tm("n1", "HOLOTYPE", col__reference_id="r1")]),
         ("name reference", [name("n1", "Aus bus", col__rank_id="SPECIES", col__reference_id="r1"), tm("n1", "HOLOTYPE")])])
    later = ["PARALECTOTYPE", "NEOTYPE", "LECTOTYPE"]
    r = rule("tw_type_designation_source", "type_material", "col__reference_id",
             "Subsequent type designation has a source", "presence", "soft", "warn",
             [cond("col__status_id", "in", later)], {},
             "Record the publication in which this lectotype, paralectotype or neotype was designated.", 30, t)
    add(None, r, [("lectotype without reference", [n, tm("n1", "LECTOTYPE")])],
        [("lectotype with reference", [n, tm("n1", "LECTOTYPE", col__reference_id="r2")])])
    r = rule("tw_type_designation_not_in_original", "type_material", "col__reference_id",
             "Subsequent type designation is not in the original publication", "related_field_not_equals",
             "soft", "warn", [cond("col__status_id", "in", later)],
             {"relation": tmn, "field": "col__reference_id", "related_field": "col__reference_id",
              "skip_if_either_empty": True},
             "A lectotype, paralectotype or neotype cannot be designated in the publication that described the name.",
             30, t)
    nr = name("n1", "Aus bus", col__rank_id="SPECIES", col__reference_id="r1")
    add(None, r, [("designated in the original", [nr, tm("n1", "LECTOTYPE", col__reference_id="r1")])],
        [("designated later", [nr, tm("n1", "LECTOTYPE", col__reference_id="r2")])])


ARTICLE = ["ARTICLE", "ARTICLE_JOURNAL", "ARTICLE_MAGAZINE", "ARTICLE_NEWSPAPER"]


def references():
    t = "Source::Bibtex"
    r = rule("tw_reference_link_format", "reference", "col__link", "Reference link is a URL", "regex", "hard",
             "error", [nonempty("col__link")], {"pattern": "(?i)(?:https?|ftp):"},
             "The link must be an http, https or ftp URL.", 20,
             f"{t} validates :url, format (app/models/source/bibtex.rb)")
    add(None, r, [("no scheme", [ref("r1", col__link="www.example.org")])],
        [("https", [ref("r1", col__link="https://www.example.org")])])
    r = rule("tw_reference_year_range", "reference", "col__issued", "Reference year is plausible", "range", "hard",
             "error", [cond("col__issued", "matches", "^[0-9]{4}")], {"min": 1000, "max": "current_year"},
             "The publication year must be between 1000 and the current year.", 20,
             f"{t} validates :year, date_year (app/models/source/bibtex.rb)")
    add(None, r, [("year 0999", [ref("r1", col__issued="0999")]), ("year 9999", [ref("r1", col__issued="9999-01")])],
        [("year 1905", [ref("r1", col__issued="1905-03-01")])])
    r = rule("tw_reference_issued_date", "reference", "col__issued", "Reference date is a valid date", "regex",
             "soft", "warn", [cond("col__issued", "matches", "^[0-9]{4}-")],
             {"pattern": "^[0-9]{4}-(?:0[1-9]|1[0-2])(?:-(?:0[1-9]|[12][0-9]|3[01]))?$"},
             "The publication date must be a year, year-month or year-month-day with a valid month and day.", 30,
             f"{t} validates :month and :day (app/models/source/bibtex.rb)")
    add(None, r, [("month 13", [ref("r1", col__issued="1905-13")]), ("day 32", [ref("r1", col__issued="1905-01-32")])],
        [("1905-03-01", [ref("r1", col__issued="1905-03-01")])])
    r = rule("tw_reference_title_markup", "reference", "col__title", "Title markup is paired", "regex", "soft",
             "warn", [cond("col__title", "contains", "<")],
             {"pattern": "^(?:[^<]|<i>[^<>]*</i>|<[^i/<]|<i[^>]|</[^i]|</i[^>])*$"},
             "The title contains an unmatched <i> or </i> tag.", 30,
             "Source#sv_html_tags and Source::Bibtex#italics_are_paired (app/models/source.rb, app/models/source/bibtex.rb)")
    add(None, r, [("unclosed", [ref("r1", col__title="New <i>Aus")])],
        [("paired", [ref("r1", col__title="New <i>Aus</i> species")])])
    r = rule("tw_reference_missing_year", "reference", "col__issued", "Reference has a year", "presence", "soft",
             "warn", None, {}, "The publication year of this reference is not recorded.", 40,
             f"{t}#sv_has_some_type_of_year and #sv_has_year ({BIB})")
    add(None, r, [("no year", [ref("r1", col__title="A title")])], [("year", [ref("r1", col__issued="1905")])])
    typed_year = ARTICLE + ["BOOK", "CHAPTER", "PAPER_CONFERENCE", "THESIS", "REPORT"]
    r = rule("tw_reference_year_before_1700", "reference", "col__issued", "Reference year is 1700 or later", "range",
             "soft", "warn", [cond("col__type_id", "in", typed_year), cond("col__issued", "matches", "^[0-9]{4}")],
             {"min": 1700}, "This publication year is before 1700; check it.", 40, f"{t}#sv_has_year ({BIB})")
    add(None, r, [("1650", [ref("r1", col__type_id="BOOK", col__issued="1650")])],
        [("1750", [ref("r1", col__type_id="BOOK", col__issued="1750")])])
    for rid, rname, field, types, extra, msg, tw, fill in [
        ("tw_reference_missing_author", "Reference has an author", "col__author",
         ARTICLE + ["BOOK", "CHAPTER", "PAPER_CONFERENCE", "THESIS", "REPORT", "MANUSCRIPT"],
         [cond("col__author_id", "equals", "")],
         "This type of reference requires an author.", "sv_has_authors", "Smith, J."),
        ("tw_reference_missing_author_or_editor", "Reference has an author or editor", "col__editor",
         ["BOOK", "CHAPTER"],
         [cond("col__author", "equals", ""), cond("col__author_id", "equals", ""), cond("col__editor_id", "equals", "")],
         "A book or book chapter needs an author or an editor.", "sv_contains_a_writer", "Jones, K."),
        ("tw_reference_missing_title", "Reference has a title", "col__title", None, [nonempty("col__type_id")],
         "This reference has no title.", "sv_has_title", "A title"),
        ("tw_reference_missing_journal", "Article has a journal", "col__container_title", ARTICLE, [],
         "This article has no journal (container title).", "sv_is_article_missing_journal", "J. Example Zool."),
        ("tw_reference_missing_publisher", "Book has a publisher", "col__publisher", ["BOOK", "CHAPTER"], [],
         "This book or book chapter has no publisher.", "sv_has_publisher", "Example Press"),
        ("tw_reference_missing_book_title", "Chapter or conference paper has a book title", "col__container_title",
         ["CHAPTER", "PAPER_CONFERENCE"], [],
         "This chapter or conference paper has no book title (container title).", "sv_has_booktitle", "Proceedings"),
        ("tw_reference_missing_pages", "Chapter has pages", "col__page", ["CHAPTER"], [],
         "This chapter has no page range.", "sv_has_chapter_or_pages", "12-20"),
        ("tw_reference_missing_institution", "Thesis or report has an institution", "col__publisher",
         ["THESIS", "REPORT"], [], "This thesis or report has no institution (publisher).",
         "sv_has_school and #sv_has_institution", "Example University"),
        ("tw_reference_missing_note", "Unpublished work has a note", "col__remarks", ["MANUSCRIPT"], [],
         "This unpublished work needs a note describing it.", "sv_has_note", "Field notebook"),
    ]:
        conds = ([cond("col__type_id", "in", types)] if types else []) + extra
        r = rule(rid, "reference", field, rname, "presence", "soft", "warn", conds, {}, msg, 40, f"{t}#{tw} ({BIB})")
        typ = types[0] if types else "BOOK"
        add(None, r, [("missing", [ref("r1", col__type_id=typ)])], [("present", [ref("r1", col__type_id=typ, **{field: fill})])])
    r = rule("tw_reference_missing_author_links", "reference", "col__author_id", "Reference authors are linked",
             "presence", "soft", "info", [nonempty("col__author")], {},
             "The authors of this reference are not linked to person records.", 50, f"{t}#sv_missing_roles ({BIB})")
    add(None, r, [("unlinked", [ref("r1", col__author="Smith, J.")])],
        [("linked", [ref("r1", col__author="Smith, J.", col__author_id="p1")])])
    r = rule("tw_reference_duplicate_title", "reference", "col__title", "Reference title is unique", "count_across",
             "soft", "info", [nonempty("col__title")],
             {"table": "reference", "match": [{"target": "col__title", "self": "col__title"}], "exclude_self": True,
              "expected": {"max": 0}},
             "Another reference has the same title; it may be a duplicate.", 40, f"{t}#sv_duplicate_title ({BIB})")
    add(None, r, [("same title", [ref("r1", col__title="A title"), ref("r2", col__title="A title")])],
        [("different titles", [ref("r1", col__title="A title"), ref("r2", col__title="Another title")])])


def build():
    for c in CODES:
        type_material(c)
    type_material_general()
    references()
