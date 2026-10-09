/**
 * Per-user interface settings.
 *
 * Settings (theme, column choices, sort order, reading positions…)
 * are stored on the server per user, so they survive clearing browser
 * data and follow the user across browsers. Pages call loadSettings()
 * once before rendering; getSetting() is then synchronous. Writes
 * update the in-memory copy immediately and reach the server after an
 * optional debounce.
 *
 * Settings saved in localStorage by earlier versions are imported on
 * first load and then removed from localStorage.
 */
import { api } from "/lib/api.js";

let cache = {};
let loading = null;
const timers = new Map();

// Earlier localStorage keys and the setting each became. Values are
// carried over unchanged.
const LEGACY_KEYS = {
  "hive-theme": "theme",
  "hive-sidebar": "sidebar",
  "hive-search-filters": "search.filters",
  "hive-standardized-authorship": "names.standardizedAuthorship",
  "hive-show-atomized": "names.showAtomized",
  "hive.reader.tagSources": "reader.tagSources",
  "hive.references.sort": "references.legacySort",
  "hive.references.steps": "references.steps",
};
const LEGACY_PREFIXES = [
  ["hive:reader-pos:", "reader.pos:"],
  // The PDF scroll key held the PDF's URL, which contains its sha.
  ["hive:pdf-scroll:/api/reference/library/", "pdf.scroll:"],
];

export function loadSettings() {
  if (!loading) {
    loading = api.settings
      .list()
      .then((s) => {
        cache = s || {};
      })
      .catch(() => {
        cache = {};
      })
      .then(importLegacy);
  }
  return loading;
}

export function getSetting(key, fallback = null) {
  return Object.prototype.hasOwnProperty.call(cache, key) ? cache[key] : fallback;
}

export function setSetting(key, value, { debounce = 0 } = {}) {
  cache[key] = value;
  clearTimeout(timers.get(key));
  timers.set(
    key,
    setTimeout(() => {
      timers.delete(key);
      api.settings.put(key, value).catch(() => {});
    }, debounce),
  );
}

export function removeSetting(key) {
  delete cache[key];
  clearTimeout(timers.get(key));
  api.settings.remove(key).catch(() => {});
}

function importLegacy() {
  let storage;
  try {
    storage = window.localStorage;
    if (!storage || storage.length === 0) return;
  } catch (e) {
    return;
  }
  const moves = [];
  for (let i = 0; i < storage.length; i++) {
    const oldKey = storage.key(i);
    let newKey = LEGACY_KEYS[oldKey];
    if (!newKey) {
      for (const [prefix, to] of LEGACY_PREFIXES) {
        if (oldKey.startsWith(prefix)) {
          newKey = to + oldKey.slice(prefix.length).replace(/\/source$/, "");
        }
      }
    }
    if (newKey) moves.push([oldKey, newKey]);
  }
  for (const [oldKey, newKey] of moves) {
    const value = storage.getItem(oldKey);
    if (value != null && !Object.prototype.hasOwnProperty.call(cache, newKey)) {
      setSetting(newKey, value);
    }
    try {
      storage.removeItem(oldKey);
    } catch (e) {
      // Leave it; the setting already carries the value.
    }
  }
}
