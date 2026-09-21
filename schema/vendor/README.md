# JATS Publishing 1.3 Relax NG — vendored

NCBI's Journal Article Tag Suite Publishing Tag Set, version 1.3
(ANSI/NISO JATS Version 1.3 — Z39.96-2021), Relax NG schema.

## Source

- Origin: <https://jats.nlm.nih.gov/publishing/1.3/> (schema files
  under `.../1.3/rng/`; the directory itself is not browsable)
- Version: 1.3 (June 2021)
- Standard: ANSI/NISO Z39.96-2021, approved 10 June 2021. The Tag
  Suite is maintained by NISO and published by NCBI/NLM. For the
  current terms of use, consult the JATS Tag Library:
  <https://jats.nlm.nih.gov/publishing/tag-library/1.3/>

## Third-party components

The `mathml2*` and `xhtml-*.mod.rng` files are W3C schemas that JATS
wraps for math and table support. They are redistributed here
unmodified, with their notices intact in the file headers:

- **MathML 2.0** (`mathml2.rng`) — Copyright © 1998-2003 W3C® (MIT,
  ERCIM, Keio), All Rights Reserved. "Permission to use, copy, modify
  and distribute the MathML 2.0 DTD and its accompanying documentation
  for any purpose and without fee is hereby granted in perpetuity,
  provided that the above copyright notice and this paragraph appear
  in all copies."
- **MathML qnames** (`mathml2-qname-1.mod.rng`) — Copyright ©
  1998-2000 W3C (MIT, INRIA, Keio), All Rights Reserved.
- **XHTML modules** (`xhtml-table-1.mod.rng`,
  `xhtml-inlstyle-1.mod.rng`) — Copyright © 1998-2005 W3C (MIT,
  ERCIM, Keio), All Rights Reserved.

The comment headers carry the required notices. Do not strip them
when refreshing.

## Files

Root schema and its modular includes; download-with-transitive-deps
was resolved from the JATS Publishing 1.3 RNG index.

## Namespacing

The canonical JATS RNG uses **no `ns=` attribute on the `<grammar>`
element**, which means the schema expects JATS elements to be in
the **null namespace** — that is, an `<article>` element with no
default `xmlns` declaration. This matches NCBI's actual PMC deposit
convention (real PMC XML does not declare `xmlns="http://jats.nlm.nih.gov"`).

The JATS namespace URI (`http://jats.nlm.nih.gov`) is used as an
identifier for XSLT keys, RDF cross-references, and documentation
— not as an XML default namespace. Any hive-emitted JATS must
follow the same convention or it will fail schema validation.

## Extending

Do not modify anything under this directory. hive's extensions live
in `../hive-jats.rng`, which uses RNG `<include>` + `combine`
overrides to widen the base content model for the `tp:` (TaxPub)
and `gn:` (hive-native metadata) namespaces. See the header comment
in `hive-jats.rng` for the extension pattern.

## Refresh

To pull a newer JATS version:

```
cd schema/vendor
rm -f *.rng
curl -sSLO 'https://jats.nlm.nih.gov/publishing/<version>/rng/JATS-journalpublishing<version-slug>.rng'
# then iteratively fetch transitively-referenced files from the
# same directory URL until no <include href="…"> is unresolved.
sha256sum *.rng > /tmp/sums
# update the checksum block below by hand.
```

## Checksums (SHA-256)

Regenerate with `sha256sum *.rng` from this directory.
690b14cd6317efcb4c8f24ad4433172527d353c93b042caeb976ef0d3a94a67f  BITS-embedded-index2.ent.rng
a8bedc391b2db77d30d6ebb724e2c607da36b4c379bde97d5b4ae23a1ee24b5a  BITS-question-answer2.ent.rng
9733aa6ee690cc1e733bccb38aa8b622fbae607775fc5716d9a3528acf7d91b1  JATS-XHTMLtablesetup1-3.ent.rng
67a54c8f3f47eb6a307810042be59c04de813a36df0a90461be7004028a1c9a9  JATS-ali-namespace1-3.ent.rng
dea0b68a95bd56bb035ee1d14ff744dde69d257b186ff65cc84ab86fa6183765  JATS-articlemeta1-3.ent.rng
d6678c301155e94da2b8094296b4fda4a3e2f47667947e2e58dca61c281bd5ba  JATS-backmatter1-3.ent.rng
15754fc50712f79a0cbb5dc00fdad9af5ada696a9b3d1989f5069f7a97e18fa3  JATS-chars1-3.ent.rng
0056d38185d389df59d5590743c460dc13d120185a58ae61ed2214e302bd92fd  JATS-common-atts1-3.ent.rng
5c651914e383f4992110e49873109cc292936deff5f8cd39f751d5ec0dacad7f  JATS-common1-3.ent.rng
2e201342ffc7cbd10d3af78ec63298ace27ace35b5e32f5a1369f792c6f5d4d0  JATS-default-classes1-3.ent.rng
5c8d231f607dca30147157f5fe02024d67eda3ae0504594edb84ca999e80a9ff  JATS-default-mixes1-3.ent.rng
851af126f64acdd31a1638e1c648983296719205e2f30db171ec0eb0b2da6677  JATS-display1-3.ent.rng
d4e1e367fa7ee66ef7c7b6b45359b937cfabaeec54cadea80342fea600d44eea  JATS-format1-3.ent.rng
a55f7bc2a306294f8104078b23a3fdc9cb89891b17ca9a92e7da477a0c127e2b  JATS-funding1-3.ent.rng
92805ae07e90aa8e72128c4a78f5392f809b4ed36ac72e6e3c74baacffa9ecdf  JATS-journalmeta1-3.ent.rng
f35b0ef3e2f6ef57dfb7e2ea3e335739e10864c3fc0ef50f165de3b8fcc408a3  JATS-journalpubcustom-classes1-3.ent.rng
5bf2029d5e469c6ba16735f3573ddd47c5e32d49b6f6582ab78dd43252a42a1d  JATS-journalpubcustom-mixes1-3.ent.rng
77336676cc141c1d192ffc24b5dca9960843f202cbf4f47be43b3938f3cc3968  JATS-journalpubcustom-models1-3.ent.rng
9a8a222dc3f8db9a5d85ace533ef6cba4b681bae62979f5424ad178002c1c3e8  JATS-journalpublishing1-3.rng
2abc899ad683e4be835ff0fc1c6938edd0b5f52d38d7a0cad126f17049739460  JATS-link1-3.ent.rng
c552525f1ea2cddfe323aaa09d63fde68b2dae859088e5adf435e076e68d82b4  JATS-list1-3.ent.rng
d3a280208c77de34a7b3e9baf60b63635ca4bc0ae9aed833042bba95066bc538  JATS-math1-3.ent.rng
a060c47fd255510585eaf0bb81c3105236a8a581e30fd2e02c7c296576b0f354  JATS-mathmlsetup1-3.ent.rng
08cd3080dff0f87ac5dffd9acb0ded6537e140847d006fd08812a07db53e2a8a  JATS-nlmcitation1-3.ent.rng
3415495ec445cacf6c8ded5e5a44c1ed711bb89fa0388ac860d2a93482f00cb7  JATS-para1-3.ent.rng
b1e8a1a6533929032ba7bea3de9273a7da758153976fc589eeadfa98ba509f36  JATS-phrase1-3.ent.rng
1b2729d17dfa7a12e84c4fb5580294ab7b4a1852e62f47c798c66ca74f4fbffe  JATS-references1-3.ent.rng
d55f30b80f326b665b2616c81ad125f01436c23e377042893fc399f201f7ed4d  JATS-related-object1-3.ent.rng
b744e11e87ae788b32e8be376c32268206290ce4c45db1f9d5f5912c8b39f58d  JATS-section1-3.ent.rng
58395cd2f9d1588ee4253b1e8d04b10caceca5f3fbc087e5b94f53c3bd9e82e2  mathml2-qname-1.mod.rng
e0501bfbef3380e1c1f3f4d86d5a55e7491e5a2933992cd2fe918afe2528ca72  mathml2.rng
9be98273e4ad63392ec5414ee123cf193227b6c6fd09f70d1c9b3e9b6007d3dc  xhtml-inlstyle-1.mod.rng
93da56a9a01e4a952dc1a0afa3cafc7d8b331355b16f742d50114668753bef64  xhtml-table-1.mod.rng
