-- Assimilate-demo existing tree seed.
--
-- Every taxon, name, author, date, institution, locality, and
-- catalogue number in this file is fabricated. Subject is the
-- fictional Order Tribbulida (tribbles). See README.md for the
-- fabrication policy.
--
-- Applies to a fresh archive after hive has run its schema and
-- vocab bootstrap (ranks, taxonomic_status, etc. must already
-- exist). Load with:
--   sqlite3 assimilate_demo.db < existing_tree.sql
--
-- All IDs are readable placeholders (n-*, t-* prefixes) rather
-- than UUIDs so the fixture stays legible when inspecting rows
-- during development.

-- ============================================================
-- References — one placeholder citation per existing taxon.
-- ============================================================

INSERT INTO reference (col__id, col__citation, col__author, col__title,
    col__container_title, col__issued, col__volume, col__page)
VALUES
  ('r-marb-1867', 'Marbleston (1867). A monograph of the Tribbulidae. Journal of Speculative Zoology 3: 12-88.',
   'Marbleston, A.', 'A monograph of the Tribbulidae',
   'Journal of Speculative Zoology', '1867', '3', '12-88'),
  ('r-whit-1969', 'Whittaker (1969). New Tribbulus from the Sherman deposit. Journal of Speculative Zoology 87: 233-241.',
   'Whittaker, J.', 'New Tribbulus from the Sherman deposit',
   'Journal of Speculative Zoology', '1969', '87', '233-241'),
  ('r-marb-1968', 'Marbleston (1968). Diagnoses of the genus Iotageminorius. Journal of Speculative Zoology 82: 141-159.',
   'Marbleston, A.', 'Diagnoses of the genus Iotageminorius',
   'Journal of Speculative Zoology', '1968', '82', '141-159'),
  ('r-coom-1972', 'Coombsworth (1972). Predatory adaptations in Glommivorus. Journal of Speculative Zoology 104: 55-70.',
   'Coombsworth, R.', 'Predatory adaptations in Glommivorus',
   'Journal of Speculative Zoology', '1972', '104', '55-70'),
  ('r-berg-1996', 'Bergwald (1996). Iridescent Tribbulidae from the deep-space stations. Iota Systematics 11: 3-24.',
   'Bergwald, H.', 'Iridescent Tribbulidae from the deep-space stations',
   'Iota Systematics', '1996', '11', '3-24');

-- ============================================================
-- Names — one row per underlying nomenclatural act.
-- ============================================================

INSERT INTO name (col__id, gn__scientific_name_string, col__scientific_name,
    col__authorship, col__rank_id, col__uninomial, col__genus, col__specific_epithet,
    gn__canonical_simple, gn__canonical_full)
VALUES
  -- Order + family + genera (uninomial ranks)
  ('n-tribbulida', 'Tribbulida', 'Tribbulida', '', 'order',
   'Tribbulida', '', '', 'Tribbulida', 'Tribbulida'),
  ('n-tribbulidae', 'Tribbulidae', 'Tribbulidae', '', 'family',
   'Tribbulidae', '', '', 'Tribbulidae', 'Tribbulidae'),
  ('n-tribbulus', 'Tribbulus', 'Tribbulus', '', 'genus',
   'Tribbulus', '', '', 'Tribbulus', 'Tribbulus'),
  ('n-iotageminorius', 'Iotageminorius', 'Iotageminorius', '', 'genus',
   'Iotageminorius', '', '', 'Iotageminorius', 'Iotageminorius'),
  ('n-glommivorus', 'Glommivorus', 'Glommivorus', '', 'genus',
   'Glommivorus', '', '', 'Glommivorus', 'Glommivorus'),
  ('n-deepspacius', 'Deepspacius', 'Deepspacius', '', 'genus',
   'Deepspacius', '', '', 'Deepspacius', 'Deepspacius'),
  -- Species (bicardinality with genus + specific epithet)
  ('n-tribbulus-tribbulus', 'Tribbulus tribbulus Marbleston, 1867',
   'Tribbulus tribbulus', 'Marbleston, 1867', 'species',
   '', 'Tribbulus', 'tribbulus', 'Tribbulus tribbulus', 'Tribbulus tribbulus'),
  ('n-tribbulus-multiplicans', 'Tribbulus multiplicans Whittaker, 1969',
   'Tribbulus multiplicans', 'Whittaker, 1969', 'species',
   '', 'Tribbulus', 'multiplicans', 'Tribbulus multiplicans', 'Tribbulus multiplicans'),
  ('n-iotageminorius-ferrugineus', 'Iotageminorius ferrugineus Marbleston, 1968',
   'Iotageminorius ferrugineus', 'Marbleston, 1968', 'species',
   '', 'Iotageminorius', 'ferrugineus', 'Iotageminorius ferrugineus', 'Iotageminorius ferrugineus'),
  ('n-glommivorus-glomivorans', 'Glommivorus glomivorans Coombsworth, 1972',
   'Glommivorus glomivorans', 'Coombsworth, 1972', 'species',
   '', 'Glommivorus', 'glomivorans', 'Glommivorus glomivorans', 'Glommivorus glomivorans'),
  ('n-deepspacius-rainbowicus', 'Deepspacius rainbowicus Bergwald, 1996',
   'Deepspacius rainbowicus', 'Bergwald, 1996', 'species',
   '', 'Deepspacius', 'rainbowicus', 'Deepspacius rainbowicus', 'Deepspacius rainbowicus');

-- ============================================================
-- Taxa — the tree the paper will be assimilated into.
--   Tribbulida (order)
--   └─ Tribbulidae (family)
--      ├─ Tribbulus (genus)
--      │  ├─ Tribbulus tribbulus (accepted)
--      │  └─ Tribbulus multiplicans (accepted)
--      ├─ Iotageminorius (genus)
--      │  └─ Iotageminorius ferrugineus (accepted)
--      ├─ Glommivorus (genus)
--      │  └─ Glommivorus glomivorans (accepted)
--      └─ Deepspacius (genus)
--         └─ Deepspacius rainbowicus (accepted)
-- ============================================================

INSERT INTO taxon (col__id, col__parent_id, col__name_id,
    col__according_to_id, col__status_id)
VALUES
  ('t-tribbulida',      '',              'n-tribbulida',      '',              'accepted'),
  ('t-tribbulidae',     't-tribbulida',  'n-tribbulidae',     '',              'accepted'),
  ('t-tribbulus',       't-tribbulidae', 'n-tribbulus',       'r-marb-1867',   'accepted'),
  ('t-iotageminorius',  't-tribbulidae', 'n-iotageminorius',  'r-marb-1968',   'accepted'),
  ('t-glommivorus',     't-tribbulidae', 'n-glommivorus',     'r-coom-1972',   'accepted'),
  ('t-deepspacius',     't-tribbulidae', 'n-deepspacius',     'r-berg-1996',   'accepted'),
  ('t-tribbulus-tribbulus',    't-tribbulus',       'n-tribbulus-tribbulus',    'r-marb-1867', 'accepted'),
  ('t-tribbulus-multiplicans', 't-tribbulus',       'n-tribbulus-multiplicans', 'r-whit-1969', 'accepted'),
  ('t-iotageminorius-ferrugineus', 't-iotageminorius', 'n-iotageminorius-ferrugineus', 'r-marb-1968', 'accepted'),
  ('t-glommivorus-glomivorans',    't-glommivorus',    'n-glommivorus-glomivorans',    'r-coom-1972', 'accepted'),
  ('t-deepspacius-rainbowicus',    't-deepspacius',    'n-deepspacius-rainbowicus',    'r-berg-1996', 'accepted');
