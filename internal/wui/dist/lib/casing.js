// Case restoration for bibliographic fields that arrive in all capitals
// ("PHYLOGENETIC POSITION OF …", "ROSALINA STANCHEVA").
//
// The optional vocabulary maps a lower-case word to the form a
// reference's own documents write it in ({zygogonium: "Zygogonium",
// rbcl: "rbcL"}); GET /api/reference/{id}/casing supplies it. Without
// it, sentence case lower-cases everything after the first word.
//
// Every function returns its input unchanged when it has nothing to
// fix, so callers can offer a fix only when the result differs.

const WORD = /\p{L}[\p{L}\p{N}'’-]*/gu;
const ROMAN = /^(?=[IVXLC]+$)M{0,3}(CM|CD|D?C{0,3})(XC|XL|L?X{0,3})(IX|IV|V?I{0,3})$/;

// isShouting reports whether s is written mostly in capitals.
export function isShouting(s) {
  const letters = (s.match(/\p{L}/gu) || []).length;
  const upper = (s.match(/\p{Lu}/gu) || []).length;
  return letters >= 4 && upper >= 0.6 * letters;
}

const cap = (w) => (w ? w[0].toUpperCase() + w.slice(1).toLowerCase() : w);

// vocabForm returns the document's form of a word, trying the whole
// token and then each hyphen-separated part.
function vocabForm(tok, vocab) {
  const low = tok.toLowerCase();
  if (vocab[low]) return vocab[low];
  if (tok.includes("-")) {
    const parts = tok.split("-");
    if (parts.some((p) => vocab[p.toLowerCase()])) {
      return parts.map((p) => vocab[p.toLowerCase()] || p.toLowerCase()).join("-");
    }
  }
  return null;
}

// sentenceCase restores an all-capitals title to sentence case: the
// first word and the first word after ":", ".", "?" or "!" are
// capitalized; other words take the vocabulary's form or lower case.
// Roman numerals stay in capitals. Text that is not mostly capitals is
// returned unchanged.
export function sentenceCase(s, vocab = {}) {
  if (!isShouting(s)) return s;
  let startNext = true;
  let last = 0;
  let out = "";
  for (const m of s.matchAll(WORD)) {
    const between = s.slice(last, m.index);
    if (/[:.?!]\s*$/.test(between) && last > 0) startNext = true;
    out += between;
    const tok = m[0];
    let w;
    if (ROMAN.test(tok) && tok.length > 1) w = tok;
    else w = vocabForm(tok, vocab) || (startNext ? cap(tok) : tok.toLowerCase());
    if (startNext && w === w.toLowerCase()) w = cap(w);
    out += w;
    startNext = false;
    last = m.index + tok.length;
  }
  return out + s.slice(last);
}

const SMALL = new Set([
  "a", "an", "and", "as", "at", "but", "by", "for", "from", "in", "into",
  "nor", "of", "on", "or", "the", "to", "via", "with", "und", "de", "des",
  "du", "et", "la", "le", "les", "für", "der", "die", "das", "y", "e",
]);

// titleCase restores an all-capitals name of a journal, publisher or
// place to title case: words capitalized, short function words in lower
// case except at the start. Vocabulary forms win. Text that is not
// mostly capitals is returned unchanged.
export function titleCase(s, vocab = {}) {
  if (!isShouting(s)) return s;
  let first = true;
  return s.replace(WORD, (tok) => {
    const low = tok.toLowerCase();
    let w = vocabForm(tok, vocab);
    if (!w) {
      if (ROMAN.test(tok) && tok.length > 1) w = tok;
      else if (!first && SMALL.has(low)) w = low;
      else w = tok.split("-").map(cap).join("-");
    }
    first = false;
    return w;
  });
}

const PARTICLES = new Set([
  "van", "von", "de", "der", "den", "da", "das", "dos", "du", "del", "della",
  "di", "la", "le", "ten", "ter", "zu", "zur", "y", "e",
]);

// nameCasePart cases one part of a name token (split on hyphen or
// apostrophe).
function nameCasePart(p) {
  const mc = /^mc(\p{L})(.*)$/iu.exec(p);
  if (mc) return "Mc" + mc[1].toUpperCase() + mc[2].toLowerCase();
  return cap(p);
}

// nameCase fixes the case of a list of personal names ("ROSALINA
// STANCHEVA; JOHN D HALL" → "Rosalina Stancheva; John D Hall"). When
// the whole field is in capitals:
//   - a common particle ("VAN", "DE") followed by more of the name, and
//     not the name's first word, becomes lower case ("Van de Vijver");
//     its real case varies by family and may still need a curator's
//     touch, since the capitals hid it;
//   - other tokens of one or two letters are kept as initials
//     ("Lewis LA", "John D Hall");
//   - everything else is capitalized per hyphen/apostrophe part, with
//     "Mc" prefixes ("McCourt").
// In fields that are not in capitals, only "Mc" prefixes are fixed
// ("Mccourt" → "McCourt").
export function nameCase(s) {
  const shouting = isShouting(s);
  return s
    .split(/(;|,|\band\b|&)/)
    .map((name) => {
      if (/^(;|,|and|&)$/.test(name)) return name;
      if (!shouting) {
        return name.replace(/(^|[\s'’-])Mc(\p{Ll})/gu, (_, pre, ch) => pre + "Mc" + ch.toUpperCase());
      }
      const parts = name.split(/(\p{L}[\p{L}'’.-]*)/u);
      // Odd indexes are tokens; even indexes are the text between.
      const tokIdx = parts.map((_, i) => i).filter((i) => i % 2 === 1);
      tokIdx.forEach((i, k) => {
        const tok = parts[i];
        const isFirst = k === 0;
        const hasNext = k < tokIdx.length - 1;
        if (!isFirst && hasNext && PARTICLES.has(tok.toLowerCase())) {
          parts[i] = tok.toLowerCase();
        } else if (tok.replace(/\./g, "").length <= 2) {
          parts[i] = tok.toUpperCase();
        } else {
          parts[i] = tok
            .split(/([-'’])/)
            .map((p) => (/^[-'’]$/.test(p) ? p : nameCasePart(p)))
            .join("");
        }
      });
      return parts.join("");
    })
    .join("");
}
