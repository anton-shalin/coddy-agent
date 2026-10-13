/**
 * The Settings search: the field, its results, and the small store the drawer
 * owns so that typing never re-renders the tab under the results.
 *
 * The field follows the documentation search's pattern (docs/DocsView.tsx): a
 * search box that is a combobox over a listbox, the highlighted result named
 * by aria-activedescendant. The results take the content column, not a
 * dropdown, because the field sits in a 200px rail.
 */
import {
  Fragment,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  useSyncExternalStore,
  type KeyboardEvent,
  type RefObject,
} from "react";
import { useT } from "../i18n/I18nProvider";
import {
  descriptionLine,
  markWords,
  searchSettings,
  searchWords,
  type SettingsSearchEntry,
  type SettingsSearchHit,
} from "./settingsSearch";

/** How long the count waits for typing to pause before it is announced. */
export const STATUS_DEBOUNCE_MS = 500;

export type SettingsSearchState = {
  query: string;
  /** Whether the results are asked for; they show when the query has a word too. */
  open: boolean;
  /** The highlighted result. */
  active: number;
  /** The result last opened, so it can be highlighted when the results come back. */
  chosenId: string | null;
  /** Counts the results opened, so choosing the same one again is a new request. */
  choice: number;
};

export type SettingsSearchStore = {
  get: () => SettingsSearchState;
  subscribe: (listener: () => void) => () => void;
  /** Typing shows the results. */
  type: (query: string) => void;
  show: (active?: number) => void;
  /** Hides the results and keeps the query. */
  hide: () => void;
  /** Forgets the query and the result chosen with it. */
  clear: () => void;
  move: (active: number) => void;
  /** Opens a result: the results hide, the query stays, the choice is remembered. */
  choose: (id: string) => void;
};

/** The state of one drawer's search; it lives and dies with the drawer. */
export function createSettingsSearch(): SettingsSearchStore {
  let state: SettingsSearchState = {
    query: "",
    open: false,
    active: 0,
    chosenId: null,
    choice: 0,
  };
  const listeners = new Set<() => void>();
  const set = (patch: Partial<SettingsSearchState>) => {
    const next = { ...state, ...patch };
    if (
      next.query === state.query &&
      next.open === state.open &&
      next.active === state.active &&
      next.chosenId === state.chosenId &&
      next.choice === state.choice
    ) {
      return;
    }
    state = next;
    listeners.forEach((listener) => listener());
  };
  return {
    get: () => state,
    subscribe: (listener) => {
      listeners.add(listener);
      return () => {
        listeners.delete(listener);
      };
    },
    type: (query) => set({ query, open: true }),
    show: (active) =>
      set(active === undefined ? { open: true } : { open: true, active }),
    hide: () => set({ open: false }),
    clear: () => set({ query: "", open: false, active: 0, chosenId: null }),
    move: (active) => set({ active }),
    choose: (id) =>
      set({ chosenId: id, open: false, choice: state.choice + 1 }),
  };
}

export function useSettingsSearchState(
  search: SettingsSearchStore,
): SettingsSearchState {
  return useSyncExternalStore(search.subscribe, search.get, search.get);
}

/** Whether the results are on screen: asked for, and the query has a word. */
function resultsShowing(state: SettingsSearchState): boolean {
  return state.open && state.query.trim() !== "";
}

/**
 * Settings reads this and nothing else of the store while it renders, so it
 * re-renders when the results start or stop showing and never as the query
 * is typed.
 */
export function useResultsShowing(search: SettingsSearchStore): boolean {
  const snapshot = () => resultsShowing(search.get());
  return useSyncExternalStore(search.subscribe, snapshot, snapshot);
}

/** The number of results opened so far; a new one is a new reveal request. */
export function useSettingsSearchChoice(search: SettingsSearchStore): number {
  const snapshot = () => search.get().choice;
  return useSyncExternalStore(search.subscribe, snapshot, snapshot);
}

// ---- what a query finds

type Found = { hits: SettingsSearchHit[]; more: number };

let lastFound: { index: unknown; query: string; found: Found } | null = null;

/**
 * The answer to a query over an index. The field and the results ask for the
 * same one in the same render, so the last answer is kept: both get the very
 * same object, which is also what tells them the hits changed.
 */
function found(index: readonly SettingsSearchEntry[], query: string): Found {
  if (
    lastFound !== null &&
    lastFound.index === index &&
    lastFound.query === query
  ) {
    return lastFound.found;
  }
  const answer = searchSettings(index, query);
  lastFound = { index, query, found: answer };
  return answer;
}

/** The result highlighted when the hits change: the chosen one if it is still there. */
function firstActive(
  hits: readonly SettingsSearchHit[],
  chosenId: string | null,
) {
  const at =
    chosenId === null ? -1 : hits.findIndex((h) => h.entry.id === chosenId);
  return at >= 0 ? at : 0;
}

function optionId(resultsId: string, at: number): string {
  return `${resultsId}-option-${at}`;
}

// ---- the field

export function SettingsSearchField(props: {
  search: SettingsSearchStore;
  index: readonly SettingsSearchEntry[];
  inputRef: RefObject<HTMLInputElement | null>;
  resultsId: string;
  disabled: boolean;
  /** The "/" hint beside an empty field: the wide drawer, where there is a keyboard. */
  showKbd: boolean;
  onOpen: (entry: SettingsSearchEntry) => void;
}) {
  const { search, index, inputRef, resultsId, disabled, showKbd, onOpen } =
    props;
  const { t } = useT();
  const state = useSettingsSearchState(search);
  const answer = found(index, state.query);
  const hits = answer.hits;
  const showing = resultsShowing(state);
  const active = Math.min(state.active, hits.length - 1);

  // When the hits change, the chosen result is highlighted if it is still
  // among them, else the first.
  useLayoutEffect(() => {
    search.move(firstActive(hits, search.get().chosenId));
  }, [search, hits]);
  // The last answer holds the whole index; it goes with the field.
  useEffect(
    () => () => {
      lastFound = null;
    },
    [],
  );

  const bringBack = () => {
    search.show(firstActive(hits, search.get().chosenId));
  };

  const onKeyDown = (e: KeyboardEvent<HTMLInputElement>) => {
    // Enter and Escape belong to the input method while it composes.
    if (e.nativeEvent.isComposing || e.keyCode === 229) {
      return;
    }
    if (e.key === "Escape") {
      if (search.get().query !== "") {
        e.preventDefault();
        search.clear();
      }
      return;
    }
    if (!showing || hits.length === 0) {
      return;
    }
    if (e.key === "ArrowDown" || e.key === "ArrowUp") {
      e.preventDefault();
      const next =
        e.key === "ArrowDown"
          ? (active + 1) % hits.length
          : (active - 1 + hits.length) % hits.length;
      search.move(next);
      document
        .getElementById(optionId(resultsId, next))
        ?.scrollIntoView?.({ block: "nearest" });
    } else if (e.key === "Enter") {
      e.preventDefault();
      const hit = hits[active];
      if (hit !== undefined) {
        onOpen(hit.entry);
      }
    }
  };

  return (
    <div className="settings-search-box">
      <input
        ref={inputRef}
        className="settings-search-input"
        data-testid="settings-search"
        type="search"
        role="combobox"
        autoComplete="off"
        aria-autocomplete="list"
        aria-controls={resultsId}
        aria-expanded={showing && hits.length > 0}
        aria-activedescendant={
          showing && active >= 0 ? optionId(resultsId, active) : undefined
        }
        placeholder={t("settings.search.placeholder")}
        aria-label={t("settings.search.placeholder")}
        value={state.query}
        disabled={disabled}
        onChange={(e) => search.type(e.target.value)}
        onFocus={bringBack}
        onClick={bringBack}
        onKeyDown={onKeyDown}
      />
      {showKbd && state.query === "" ? (
        <kbd className="settings-search-kbd" aria-hidden="true">
          /
        </kbd>
      ) : null}
    </div>
  );
}

// ---- the results

/** A text with the words of the query marked. */
function Marked(props: { text: string; words: readonly string[] }) {
  return (
    <>
      {markWords(props.text, props.words).map((part, i) =>
        part.hit ? (
          <mark key={i}>{part.text}</mark>
        ) : (
          <Fragment key={i}>{part.text}</Fragment>
        ),
      )}
    </>
  );
}

/** The description that holds a word of the query, else the first one. */
function describing(
  entry: SettingsSearchEntry,
  words: readonly string[],
): string | null {
  const holding = entry.descriptions.find((d) =>
    markWords(d, words).some((part) => part.hit),
  );
  return holding ?? entry.descriptions[0] ?? null;
}

export function SettingsSearchResults(props: {
  id: string;
  search: SettingsSearchStore;
  index: readonly SettingsSearchEntry[];
  onOpen: (entry: SettingsSearchEntry) => void;
}) {
  const { id, search, index, onOpen } = props;
  const { t, tp } = useT();
  const state = useSettingsSearchState(search);
  const { hits, more } = found(index, state.query);
  const words = useMemo(() => searchWords(state.query), [state.query]);
  const active = Math.min(state.active, hits.length - 1);

  // The highlighted result stays in view as the hits change and the arrows
  // move; a pointer moving it needs no scrolling.
  const byPointer = useRef(false);
  useEffect(() => {
    if (byPointer.current) {
      byPointer.current = false;
      return;
    }
    if (active >= 0) {
      document
        .getElementById(optionId(id, active))
        ?.scrollIntoView?.({ block: "nearest" });
    }
  }, [id, active, hits]);

  // The count is announced once typing pauses.
  const total = hits.length + more;
  const count = tp("settings.search.found", total);
  const [announced, setAnnounced] = useState("");
  useEffect(() => {
    const timer = window.setTimeout(
      () => setAnnounced(count),
      STATUS_DEBOUNCE_MS,
    );
    return () => window.clearTimeout(timer);
  }, [count]);

  return (
    <div
      className="settings-scroll settings-search-results"
      data-testid="settings-search-results"
    >
      <span className="sr-only" role="status">
        {announced}
      </span>
      {hits.length === 0 ? (
        <p
          className="settings-search-empty"
          data-testid="settings-search-empty"
        >
          {t("settings.search.empty")}
        </p>
      ) : (
        <ul
          className="settings-hits"
          id={id}
          role="listbox"
          aria-label={t("settings.search.results")}
          data-testid="settings-search-hits"
        >
          {hits.map((hit, i) => {
            const { entry } = hit;
            const description =
              hit.tier === 3 ? describing(entry, words) : null;
            const descId = `${optionId(id, i)}-desc`;
            const key = entry.keyPath;
            return (
              <li key={entry.id} role="presentation">
                <button
                  type="button"
                  id={optionId(id, i)}
                  role="option"
                  tabIndex={-1}
                  aria-selected={i === active}
                  aria-describedby={description !== null ? descId : undefined}
                  className={`settings-hit${i === active ? " is-selected" : ""}`}
                  data-settings-target={entry.anchor ?? `tab:${entry.section}`}
                  data-settings-row={entry.row ?? undefined}
                  onMouseEnter={() => {
                    byPointer.current = true;
                    search.move(i);
                  }}
                  onClick={() => onOpen(entry)}
                >
                  <span className="settings-hit-title">
                    <Marked text={entry.names[0] ?? ""} words={words} />
                    {hit.secondMatched !== null ? (
                      <>
                        {" · "}
                        <Marked text={hit.secondMatched} words={words} />
                      </>
                    ) : null}
                  </span>
                  {entry.where.length > 0 || key !== null ? (
                    <span className="settings-hit-where">
                      {entry.where.length > 0 ? (
                        <Marked text={entry.where.join(" › ")} words={words} />
                      ) : null}
                      {key !== null ? (
                        <>
                          {entry.where.length > 0 ? " " : null}
                          <code className="settings-hit-key">
                            <Marked text={key} words={words} />
                          </code>
                        </>
                      ) : null}
                    </span>
                  ) : null}
                  {description !== null ? (
                    <span
                      id={descId}
                      className="settings-hit-desc"
                      aria-hidden="true"
                    >
                      <Marked
                        text={descriptionLine(description, words)}
                        words={words}
                      />
                    </span>
                  ) : null}
                </button>
              </li>
            );
          })}
        </ul>
      )}
      {more > 0 ? (
        <p className="settings-search-more" data-testid="settings-search-more">
          {tp("settings.search.more", more)}
        </p>
      ) : null}
    </div>
  );
}
