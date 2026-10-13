import {
  afterEach,
  beforeEach,
  describe,
  expect,
  onTestFinished,
  test,
  vi,
} from "vitest";
import { useEffect, useLayoutEffect, useState } from "react";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { Settings } from "./Settings";
import {
  noteSettingsConfigReloaded,
  resetSettingsConfigForTests,
} from "./settingsConfigStore";
import { SEARCH_TARGET_MS } from "./settingsReveal";
import { parseAppHash } from "../scheduler/hashRoute";
import { initLocale } from "../i18n/i18n";
import { OpenRailScreen } from "../nav/railEscape.fakes";
import { pressBack } from "../telegram/backButton";
import {
  AGENT_SCHEMA,
  RELAY_SCHEMA,
  configFromSchema,
  mountSettings,
  openSettings,
  resetPage,
  stubServer as stubServerFor,
  type MountOptions,
  type ServerState,
} from "./settingsServer.fakes";

const schema = {
  type: "object",
  "x-coddy-property-order": ["providers", "models", "agent"],
  properties: {
    providers: {
      type: "array",
      title: "LLM providers",
      items: {
        type: "object",
        properties: { name: { type: "string", title: "Provider id" } },
      },
    },
    models: {
      type: "array",
      title: "Logical models",
      items: {
        type: "object",
        properties: { model: { type: "string", title: "Model id" } },
      },
    },
    agent: {
      type: "object",
      title: "ReAct loop",
      properties: { max_turns: { type: "integer", title: "Max turns" } },
    },
  },
};

let server: ServerState;

/** A coddy serve behind fetch, reading the state this file keeps in `server`. */
function stubServer() {
  return stubServerFor(() => server);
}

function configReads(fetch: ReturnType<typeof stubServer>): string[] {
  return fetch.mock.calls
    .filter(
      ([path, init]) =>
        String(path).startsWith("/coddy/config") &&
        ((init as RequestInit | undefined)?.method ?? "GET") === "GET",
    )
    .map(([path]) => String(path));
}

beforeEach(() => {
  initLocale("en");
  resetSettingsConfigForTests();
  server = {
    schema,
    config: {
      providers: [{ name: "demo" }],
      models: [{ model: "demo/m1" }, { model: "demo/m2" }],
      agent: { max_turns: 40 },
    },
    put: { status: 200, body: { ok: true } },
  };
  window.location.hash = "";
});

afterEach(() => {
  cleanup();
  vi.useRealTimers();
  vi.unstubAllGlobals();
  resetSettingsConfigForTests();
});

// Issue #359: Settings is mounted only while it is open, and every mount read the
// schema and the config again, starting from nothing - so each open drew the
// drawer empty (Appearance and Sessions only), then rebuilt it. The app keeps the
// copy it read, and a second open draws the tab from it in its first frame.
test("reopening Settings draws the tab from the copy it keeps, without waiting for a request", async () => {
  const fetch = stubServer();
  const first = render(
    <Settings onClose={() => {}} initialSection="providers" />,
  );
  await screen.findByTestId("settings-master-item-0");
  first.unmount();
  fetch.mockClear();

  const { container } = render(
    <Settings onClose={() => {}} initialSection="providers" />,
  );

  // The first frame, with nothing awaited: no Appearance in between.
  expect(screen.getByTestId("settings-master-item-0").textContent).toBe("demo");
  expect(container.querySelector(".appearance-swatch-grid")).toBeNull();
  expect(
    screen.getByTestId("settings-tab-providers").getAttribute("aria-current"),
  ).toBe("page");
  expect(configReads(fetch)).toEqual([]);
});

// The very first open has nothing to draw from. It shows the tab the address
// asks for as a skeleton, not the Appearance tab that needs no schema.
test("the first open shows the requested tab as a skeleton, not Appearance", () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(() => new Promise<Response>(() => {})),
  );
  const { container } = render(
    <Settings onClose={() => {}} initialSection="providers" />,
  );

  expect(screen.getByTestId("settings-skeleton")).toBeTruthy();
  expect(container.querySelector(".appearance-swatch-grid")).toBeNull();
  expect(
    screen.getByTestId("settings-tab-appearance").getAttribute("aria-current"),
  ).toBeNull();
});

test("on the narrow shell the first open shows the requested section as a skeleton, not the tiles", () => {
  const original = window.matchMedia;
  window.matchMedia = vi.fn().mockImplementation((query: string) => ({
    matches: query.includes("max-width"),
    media: query,
    onchange: null,
    addListener: vi.fn(),
    removeListener: vi.fn(),
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
    dispatchEvent: vi.fn(),
  })) as unknown as typeof window.matchMedia;
  try {
    vi.stubGlobal(
      "fetch",
      vi.fn(() => new Promise<Response>(() => {})),
    );
    const { container } = render(
      <Settings onClose={() => {}} initialSection="providers" />,
    );

    expect(screen.getByTestId("settings-skeleton")).toBeTruthy();
    expect(screen.queryByTestId("settings-tile-grid")).toBeNull();
    expect(container.querySelector(".settings-head-section")?.textContent).toBe(
      "LLM providers",
    );
  } finally {
    window.matchMedia = original;
  }
});

// Escape takes the step the head's arrow takes while the head shows one: a
// section on the narrow shell goes back to the tiles, one still loading too,
// and only then does the drawer close.
test("on the narrow shell Escape takes a section, even one still loading, back to the tiles first", () => {
  const original = window.matchMedia;
  window.matchMedia = vi.fn().mockImplementation((query: string) => ({
    matches: query.includes("max-width"),
    media: query,
    onchange: null,
    addListener: vi.fn(),
    removeListener: vi.fn(),
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
    dispatchEvent: vi.fn(),
  })) as unknown as typeof window.matchMedia;
  try {
    vi.stubGlobal(
      "fetch",
      vi.fn(() => new Promise<Response>(() => {})),
    );
    const closeDrawer = vi.fn();
    render(
      <>
        <OpenRailScreen id="settings" onClose={closeDrawer} />
        <Settings onClose={closeDrawer} initialSection="providers" />
      </>,
    );
    expect(screen.getByTestId("settings-skeleton")).toBeTruthy();

    fireEvent.keyDown(document.body, { key: "Escape" });
    expect(screen.getByTestId("settings-tile-grid")).toBeTruthy();
    expect(closeDrawer).not.toHaveBeenCalled();

    fireEvent.keyDown(document.body, { key: "Escape" });
    expect(closeDrawer).toHaveBeenCalledTimes(1);
  } finally {
    window.matchMedia = original;
  }
});

// The kept copy follows the server: config_reloaded reads it again in the
// background. An open form takes the new copy while it holds no edits of its
// own; with unsaved edits it keeps them, and the next open starts from the new
// copy.
test("a configuration reload reaches the kept copy and an untouched open form, never unsaved edits", async () => {
  stubServer();
  const view = render(<Settings onClose={() => {}} initialSection="agent" />);
  const input = (await screen.findByLabelText("Max turns")) as HTMLInputElement;
  expect(input.value).toBe("40");

  server.config = { ...server.config, agent: { max_turns: 60 } };
  act(() => noteSettingsConfigReloaded());
  await waitFor(() => expect(input.value).toBe("60"));

  fireEvent.change(input, { target: { value: "45" } });
  server.config = { ...server.config, agent: { max_turns: 70 } };
  act(() => noteSettingsConfigReloaded());
  await act(async () => {
    await new Promise((r) => setTimeout(r, 20));
  });
  expect(input.value).toBe("45");

  view.unmount();
  render(<Settings onClose={() => {}} initialSection="agent" />);
  expect((screen.getByLabelText("Max turns") as HTMLInputElement).value).toBe(
    "70",
  );
});

// A save says it worked on the Save button alone: green for a couple of seconds,
// no line of text. The line used to stay on screen for as long as the drawer did.
test("a save lights the Save button green for two seconds and writes no text", async () => {
  vi.useFakeTimers({ shouldAdvanceTime: true });
  stubServer();
  const { container } = render(
    <Settings onClose={() => {}} initialSection="agent" />,
  );
  await screen.findByLabelText("Max turns");
  const save = screen.getByTestId("settings-save");

  await act(async () => {
    fireEvent.click(save);
  });
  await waitFor(() => expect(save.className).toContain("is-saved"));
  expect(container.querySelector(".settings-lead-pane")).toBeNull();
  expect(container.querySelector(".settings-ok")).toBeNull();

  await act(async () => {
    await vi.advanceTimersByTimeAsync(2100);
  });
  expect(save.className).not.toContain("is-saved");
});

// A failed save reports what went wrong where the status band stands, in words,
// and the Save button does not pretend it worked.
test("a failed save shows the error in the status band and leaves the button as it was", async () => {
  stubServer();
  server.put = {
    status: 400,
    body: { ok: false, error: "models[0].model: provider demo is not listed" },
  };
  const { container } = render(
    <Settings onClose={() => {}} initialSection="agent" />,
  );
  await screen.findByLabelText("Max turns");
  const save = screen.getByTestId("settings-save");

  await act(async () => {
    fireEvent.click(save);
  });

  await waitFor(() =>
    expect(
      container.querySelector(".settings-lead-pane .settings-error")
        ?.textContent,
    ).toBe("models[0].model: provider demo is not listed"),
  );
  expect(save.className).not.toContain("is-saved");
});

/**
 * Settings the way App mounts it: the section and the row come from the address,
 * which the drawer itself rewrites and App reads back on hashchange.
 */
function RoutedSettings() {
  const read = () => {
    const route = parseAppHash();
    return route.branch === "settings"
      ? { section: route.section, item: route.item }
      : { section: null, item: null };
  };
  const [route, setRoute] = useState(read);
  useEffect(() => {
    const on = () => setRoute(read());
    window.addEventListener("hashchange", on);
    return () => window.removeEventListener("hashchange", on);
  }, []);
  return (
    <Settings
      onClose={() => {}}
      initialSection={route.section}
      initialItem={route.item}
    />
  );
}

// Issue #359: with a provider open, going to Logical models opened the first
// model instead of the list (00dbbee6 fixed the path through SettingsSection;
// this holds the drawer with its address).
test("going to Logical models with a provider open shows the list of models", async () => {
  stubServer();
  window.location.hash = "#/settings/providers";
  const { container } = render(<RoutedSettings />);
  fireEvent.click(await screen.findByTestId("settings-master-item-0"));
  await waitFor(() =>
    expect(window.location.hash).toBe("#/settings/providers?id=demo"),
  );

  fireEvent.click(screen.getByTestId("settings-tab-models"));

  await waitFor(() => expect(window.location.hash).toBe("#/settings/models"));
  expect(screen.getByTestId("settings-master-item-0").textContent).toBe(
    "demo/m1",
  );
  expect(container.querySelector(".settings-detail")).toBeNull();
  expect(container.querySelector(".settings-head-section")).toBeNull();
});

// The first open with an address naming a row: the tab is drawn from the first
// read once it lands, rows and all, so the row opens and the address keeps it.
// A frame drawn from an empty document in between would show the list with no
// rows, and the list takes an address naming a row it lacks for a stale one.
test("a first open at an address naming a row opens that row and keeps the address", async () => {
  stubServer();
  window.location.hash = "#/settings/models?id=demo%2Fm2";
  const writes: string[] = [];
  const replace = history.replaceState.bind(history);
  const spy = vi
    .spyOn(history, "replaceState")
    .mockImplementation((data, unused, url) => {
      writes.push(String(url ?? ""));
      replace(data, unused, url);
    });
  try {
    const { container } = render(<RoutedSettings />);
    // The model field shows the id after the provider's name and its slash.
    await waitFor(() =>
      expect(
        (screen.getByLabelText("Model id") as HTMLInputElement).value,
      ).toBe("m2"),
    );
    expect(container.querySelector(".settings-head-section")?.textContent).toBe(
      "Model settings",
    );
    expect(window.location.hash).toBe("#/settings/models?id=demo%2Fm2");
    expect(writes).toEqual([]);
  } finally {
    spy.mockRestore();
  }
});

// After a save the form shows what it saved, while the copy read after the save
// is still on its way - never the copy from before the save.
test("a save keeps its values on screen until the fresh copy lands", async () => {
  const fetch = stubServer();
  render(<Settings onClose={() => {}} initialSection="agent" />);
  const input = (await screen.findByLabelText("Max turns")) as HTMLInputElement;
  fireEvent.change(input, { target: { value: "45" } });

  // Hold the reads that come after the save.
  let release: () => void = () => {};
  const held = new Promise<void>((r) => {
    release = r;
  });
  const answer = fetch.getMockImplementation()!;
  fetch.mockImplementation(async (path: string, init?: RequestInit) => {
    if ((init?.method ?? "GET") === "GET" && path.startsWith("/coddy/config")) {
      await held;
    }
    return answer(path, init);
  });

  await act(async () => {
    fireEvent.click(screen.getByTestId("settings-save"));
  });
  await waitFor(() =>
    expect(screen.getByTestId("settings-save").className).toContain("is-saved"),
  );
  expect(input.value).toBe("45");

  await act(async () => {
    release();
    await held;
  });
  await waitFor(() => expect(input.value).toBe("45"));
  expect(server.config).toMatchObject({ agent: { max_turns: 45 } });
});

// The copy the app keeps takes what a save wrote at once. A reopen before the
// read after the save lands - or after that read failed - draws the saved
// values, never the ones the save replaced: saving again from those would put
// them back.
test("a reopen right after a save draws what was saved, even when the read after it fails", async () => {
  const fetch = stubServer();
  const view = render(<Settings onClose={() => {}} initialSection="agent" />);
  const input = (await screen.findByLabelText("Max turns")) as HTMLInputElement;
  fireEvent.change(input, { target: { value: "45" } });

  const answer = fetch.getMockImplementation()!;
  fetch.mockImplementation(async (path: string, init?: RequestInit) => {
    if ((init?.method ?? "GET") === "GET" && path.startsWith("/coddy/config")) {
      return {
        ok: false,
        status: 502,
        json: async () => ({}),
      } as unknown as Awaited<ReturnType<typeof answer>>;
    }
    return answer(path, init);
  });
  await act(async () => {
    fireEvent.click(screen.getByTestId("settings-save"));
  });
  await waitFor(() =>
    expect(screen.getByTestId("settings-save").className).toContain("is-saved"),
  );
  view.unmount();

  render(<Settings onClose={() => {}} initialSection="agent" />);
  expect((screen.getByLabelText("Max turns") as HTMLInputElement).value).toBe(
    "45",
  );
});

// Reload drops the edits the form held when it was pressed; a field typed
// into while its read is on its way keeps what was typed.
test("edits typed while Reload is on its way stay", async () => {
  vi.useFakeTimers({ shouldAdvanceTime: true });
  const fetch = stubServer();
  render(<Settings onClose={() => {}} initialSection="agent" />);
  await screen.findByLabelText("Max turns");

  let release: () => void = () => {};
  const held = new Promise<void>((r) => {
    release = r;
  });
  const answer = fetch.getMockImplementation()!;
  fetch.mockImplementation(async (path: string, init?: RequestInit) => {
    if ((init?.method ?? "GET") === "GET" && path.startsWith("/coddy/config")) {
      await held;
    }
    return answer(path, init);
  });
  await act(async () => {
    fireEvent.click(screen.getByTestId("settings-reload"));
  });
  fireEvent.change(screen.getByLabelText("Max turns"), {
    target: { value: "55" },
  });
  await act(async () => {
    release();
    await vi.advanceTimersByTimeAsync(600);
  });

  expect((screen.getByLabelText("Max turns") as HTMLInputElement).value).toBe(
    "55",
  );
});

// A save that fails right after one that went through does not keep the green
// of the first: the band says what went wrong and the button is back to itself.
test("a failed save right after a successful one leaves the button as it was", async () => {
  stubServer();
  const { container } = render(
    <Settings onClose={() => {}} initialSection="agent" />,
  );
  await screen.findByLabelText("Max turns");
  const save = screen.getByTestId("settings-save");
  await act(async () => {
    fireEvent.click(save);
  });
  await waitFor(() => expect(save.className).toContain("is-saved"));

  server.put = { status: 400, body: { ok: false, error: "agent: refused" } };
  await act(async () => {
    fireEvent.click(save);
  });
  await waitFor(() =>
    expect(
      container.querySelector(".settings-lead-pane .settings-error")
        ?.textContent,
    ).toBe("agent: refused"),
  );
  expect(save.className).not.toContain("is-saved");
});

// A first read that failed is read again by the next open. Until that read
// ends, the requested tab shows as loading - the error of the attempt before
// is not the state of this one, and Appearance does not stand in for the tab.
test("a reopen after a failed first read shows the requested tab loading, not Appearance", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => ({ ok: false, status: 502, json: async () => ({}) })),
  );
  const first = render(
    <Settings onClose={() => {}} initialSection="providers" />,
  );
  await waitFor(() =>
    expect(
      first.container.querySelector(".settings-lead-pane .settings-error"),
    ).toBeTruthy(),
  );
  first.unmount();

  vi.stubGlobal(
    "fetch",
    vi.fn(() => new Promise<Response>(() => {})),
  );
  // What the first frame shows, before the effects that start the new read.
  const frames: { skeleton: boolean; appearance: boolean }[] = [];
  function FirstFrame() {
    useLayoutEffect(() => {
      frames.push({
        skeleton:
          document.querySelector('[data-testid="settings-skeleton"]') !== null,
        appearance: document.querySelector(".appearance-swatch-grid") !== null,
      });
    }, []);
    return null;
  }
  const { container } = render(
    <>
      <Settings onClose={() => {}} initialSection="providers" />
      <FirstFrame />
    </>,
  );
  expect(frames).toEqual([{ skeleton: true, appearance: false }]);
  await act(async () => {});
  expect(screen.getByTestId("settings-skeleton")).toBeTruthy();
  expect(container.querySelector(".settings-lead-pane")).toBeNull();
});

// A save that ends after the operator typed on saved what it sent, not what the
// form shows now: the button does not turn green, and the form still holds
// edits a reload of the server's config leaves alone.
test("a save during which the operator typed on does not turn the button green", async () => {
  const fetch = stubServer();
  render(<Settings onClose={() => {}} initialSection="agent" />);
  const input = (await screen.findByLabelText("Max turns")) as HTMLInputElement;
  fireEvent.change(input, { target: { value: "45" } });

  let release: () => void = () => {};
  const held = new Promise<void>((r) => {
    release = r;
  });
  const answer = fetch.getMockImplementation()!;
  fetch.mockImplementation(async (path: string, init?: RequestInit) => {
    if (init?.method === "PUT") {
      await held;
    }
    return answer(path, init);
  });
  const save = screen.getByTestId("settings-save");
  await act(async () => {
    fireEvent.click(save);
  });
  fireEvent.change(input, { target: { value: "46" } });
  await act(async () => {
    release();
    await held;
  });
  await waitFor(() =>
    expect(server.config).toMatchObject({ agent: { max_turns: 45 } }),
  );
  await act(async () => {
    await new Promise((r) => setTimeout(r, 20));
  });

  expect(save.className).not.toContain("is-saved");
  expect(input.value).toBe("46");
});

// A reload of the server's config reached an open provider form that holds no
// edits, and the providers came back in another order. The form stays on the
// provider the address names instead of showing whichever one took its place.
test("an open row form keeps its row when a reload reorders the list", async () => {
  stubServer();
  server.config = {
    ...server.config,
    providers: [{ name: "demo" }, { name: "spare" }],
  };
  window.location.hash = "#/settings/providers?id=spare";
  render(<RoutedSettings />);
  await waitFor(() =>
    expect(
      (screen.getByLabelText("Provider id") as HTMLInputElement).value,
    ).toBe("spare"),
  );

  server.config = {
    ...server.config,
    providers: [{ name: "spare" }, { name: "demo" }],
  };
  act(() => noteSettingsConfigReloaded());
  await act(async () => {
    await new Promise((r) => setTimeout(r, 20));
  });

  expect((screen.getByLabelText("Provider id") as HTMLInputElement).value).toBe(
    "spare",
  );
  expect(window.location.hash).toBe("#/settings/providers?id=spare");
});

// A newer copy the untouched form takes while a save runs is no edit of the
// operator's: the save still turns the button green, and the form keeps
// following the server's config afterwards.
test("a copy taken while a save runs does not keep the button from turning green", async () => {
  const fetch = stubServer();
  render(<Settings onClose={() => {}} initialSection="agent" />);
  const input = (await screen.findByLabelText("Max turns")) as HTMLInputElement;

  let release: () => void = () => {};
  const held = new Promise<void>((r) => {
    release = r;
  });
  const answer = fetch.getMockImplementation()!;
  fetch.mockImplementation(async (path: string, init?: RequestInit) => {
    if (init?.method === "PUT") {
      await held;
    }
    return answer(path, init);
  });
  const save = screen.getByTestId("settings-save");
  await act(async () => {
    fireEvent.click(save);
  });
  server.config = { ...server.config, agent: { max_turns: 41 } };
  act(() => noteSettingsConfigReloaded());
  await waitFor(() => expect(input.value).toBe("41"));
  await act(async () => {
    release();
    await held;
  });
  await waitFor(() => expect(save.className).toContain("is-saved"));

  server.config = { ...server.config, agent: { max_turns: 43 } };
  act(() => noteSettingsConfigReloaded());
  await waitFor(() => expect(input.value).toBe("43"));
});

// ---- the settings search

/**
 * The page's animation frames, run by hand. A reveal waits a frame after the
 * page can show a setting, and jsdom's own frames fire late and in a burst when
 * they are asked for under act.
 */
function frameClock() {
  const queue = new Map<number, FrameRequestCallback>();
  let next = 0;
  const request = vi
    .spyOn(window, "requestAnimationFrame")
    .mockImplementation((callback) => {
      queue.set(++next, callback);
      return next;
    });
  const cancel = vi
    .spyOn(window, "cancelAnimationFrame")
    .mockImplementation((id) => {
      queue.delete(id);
    });
  return {
    /** Lets the observers see what changed and runs the frames asked for, until the page is quiet. */
    async settle() {
      for (let round = 0; round < 8; round++) {
        await act(async () => {
          await Promise.resolve();
          await Promise.resolve();
        });
        const due = [...queue.values()];
        queue.clear();
        if (due.length > 0) {
          await act(async () => {
            due.forEach((callback) => callback(performance.now()));
          });
        }
      }
    },
    restore() {
      request.mockRestore();
      cancel.mockRestore();
    },
  };
}

/** The drawer, open on a page that follows its address the way App's does. */
function drawer(opts: MountOptions = {}) {
  return openSettings({ routed: true, ...opts });
}

const searchInput = () =>
  document.querySelector<HTMLInputElement>(".settings-search-input")!;

function typeQuery(text: string) {
  fireEvent.change(searchInput(), { target: { value: text } });
}

const options = () => [
  ...document.querySelectorAll<HTMLElement>('[role="option"]'),
];

/** The result for a box (and a row): its dataset says which it is. */
function optionOf(anchor: string, row?: string): HTMLElement {
  const found = options().find(
    (o) =>
      o.dataset["settingsTarget"] === anchor &&
      (row === undefined || o.dataset["settingsRow"] === row),
  );
  if (found === undefined) {
    throw new Error(`no result for ${anchor} ${row ?? ""}`);
  }
  return found;
}

/** The first box of the page that carries an anchor, in document order. */
function boxOf(anchor: string, scope: ParentNode = document) {
  return (
    [...scope.querySelectorAll<HTMLElement>("[data-settings-anchor]")].find(
      (b) => b.dataset["settingsAnchor"] === anchor,
    ) ?? null
  );
}

const rowForm = (row: string) =>
  [...document.querySelectorAll<HTMLElement>(".settings-detail")].find(
    (form) => form.dataset["settingsRow"] === row,
  ) ?? null;

const headTitle = () =>
  document.querySelector(".sessions-head")!.firstElementChild!.textContent;

/** Answers some GET routes of the stubbed server with the given bodies. */
function answering(
  fetch: ReturnType<typeof stubServerFor>,
  routes: Record<string, unknown>,
) {
  const base = fetch.getMockImplementation()!;
  fetch.mockImplementation((async (path: string, init?: RequestInit) => {
    const route = new URL(String(path), "http://coddy.test").pathname;
    if (route in routes && (init?.method ?? "GET") === "GET") {
      return {
        ok: true,
        status: 200,
        json: async () => structuredClone(routes[route]),
      };
    }
    return base(path, init);
  }) as never);
}

/**
 * Fake timers that leave the animation frames to frameClock: the fake clock
 * would take requestAnimationFrame over and hand it back to the spy.
 */
function useClockWithFrames() {
  vi.useFakeTimers({
    shouldAdvanceTime: true,
    toFake: [
      "setTimeout",
      "clearTimeout",
      "setInterval",
      "clearInterval",
      "Date",
    ],
  });
}

describe("settings search", () => {
  let scrolled: ReturnType<typeof vi.fn>;
  let frames: ReturnType<typeof frameClock>;
  /** What a reveal scrolled to (the middle of the area), in order. */
  const scrolledTo = (block = "center") =>
    scrolled.mock.contexts.filter(
      (_, i) =>
        (scrolled.mock.calls[i]?.[0] as { block?: string })?.block === block,
    ) as Element[];

  beforeEach(() => {
    scrolled = vi.fn();
    Element.prototype.scrollIntoView = scrolled;
    frames = frameClock();
  });

  afterEach(() => {
    delete (Element.prototype as { scrollIntoView?: unknown }).scrollIntoView;
    frames.restore();
    resetPage();
  });

  test("slash puts the cursor in the settings search unless a field has it or a dialog is open", async () => {
    await drawer({ section: "agent" });
    const slash = (
      target: Element = document.body,
      init: KeyboardEventInit = {},
    ) => fireEvent.keyDown(target, { key: "/", ...init });

    // Nothing has the cursor: the search takes it, and no "/" is typed.
    expect(slash()).toBe(false);
    expect(document.activeElement).toBe(searchInput());
    act(() => searchInput().blur());

    // A modifier makes it a shortcut that belongs to the browser.
    expect(slash(document.body, { ctrlKey: true })).toBe(true);
    expect(slash(document.body, { metaKey: true })).toBe(true);
    expect(document.activeElement).toBe(document.body);

    // A field with the cursor takes the character.
    const field = screen.getByLabelText("Max turns");
    act(() => field.focus());
    expect(slash(field)).toBe(true);
    expect(document.activeElement).toBe(field);
    act(() => field.blur());

    // A dialog is open: the cursor stays where it was.
    const dialog = document.createElement("div");
    dialog.setAttribute("aria-modal", "true");
    document.body.appendChild(dialog);
    expect(slash()).toBe(true);
    expect(document.activeElement).toBe(document.body);
    dialog.remove();
  });

  test("on a narrow screen with a tab open, slash goes back to the tiles and puts the cursor in the field once it is drawn", async () => {
    await drawer({ width: 390, section: "agent" });
    // The tab is open and the field is not drawn over it.
    expect(searchInput()).toBeNull();
    expect(screen.getByLabelText("Max turns")).toBeTruthy();

    expect(fireEvent.keyDown(document.body, { key: "/" })).toBe(false);

    expect(screen.getByTestId("settings-tile-grid")).toBeTruthy();
    expect(window.location.hash).toBe("#/settings");
    expect(document.activeElement).toBe(searchInput());
    // On the tiles the field takes the cursor at once.
    act(() => searchInput().blur());
    expect(fireEvent.keyDown(document.body, { key: "/" })).toBe(false);
    expect(document.activeElement).toBe(searchInput());
  });

  test("typing a query shows the matching settings of every tab in place of the sections, with the words marked", async () => {
    const view = await drawer({ section: "agent" });
    expect(searchInput().disabled).toBe(false);
    expect(screen.queryByRole("listbox")).toBeNull();

    typeQuery("proxy");

    const list = screen.getByRole("listbox", { name: "Matching settings" });
    const found = within(list).getAllByRole("option");
    const targets = found.map(
      (o) => `${o.dataset["settingsTarget"]}|${o.dataset["settingsRow"] ?? ""}`,
    );
    expect(targets).toContain("providers:proxy|openai");
    expect(targets).toContain("providers:proxy|anthropic");
    expect(targets).toContain("gateways:telegram.proxy|");
    // The words of the query are marked wherever they stand.
    const marked = [...found[0]!.querySelectorAll("mark")].map((m) =>
      m.textContent!.toLowerCase(),
    );
    expect(marked.length).toBeGreaterThan(0);
    expect(marked.every((m) => m === "proxy")).toBe(true);
    // They stand in place of the sections: the rail names no tab, and the tab
    // under them is hidden, not unmounted.
    expect(document.querySelector('[aria-current="page"]')).toBeNull();
    const tab = document.querySelector<HTMLElement>(
      '[data-settings-tab="agent"]',
    )!;
    expect(tab.hidden).toBe(true);
    expect(screen.getByLabelText("Max turns")).toBeTruthy();
    expect(within(tab).queryByRole("listbox")).toBeNull();
    expect(searchInput().getAttribute("aria-expanded")).toBe("true");
    view.unmount();

    // The field waits for the first read.
    const first = mountSettings({ routed: true });
    expect(searchInput().disabled).toBe(true);
    await waitFor(() => expect(searchInput().disabled).toBe(false));
    first.unmount();
  });

  test("choosing a field in another tab opens that tab, rings the setting and puts the cursor in it", async () => {
    useClockWithFrames();
    await drawer();
    typeQuery("max_turns");

    fireEvent.click(optionOf("agent:max_turns"));

    expect(
      screen.getByTestId("settings-tab-agent").getAttribute("aria-current"),
    ).toBe("page");
    expect(window.location.hash).toBe("#/settings/agent");
    // The results are away and the query is kept.
    expect(screen.queryByRole("listbox")).toBeNull();
    expect(searchInput().value).toBe("max_turns");

    await frames.settle();
    const box = boxOf("agent:max_turns")!;
    expect(box.classList.contains("is-search-target")).toBe(true);
    expect(scrolledTo()).toContain(box);
    expect(scrolled).toHaveBeenCalledWith({
      block: "center",
      behavior: "smooth",
    });
    expect(document.activeElement).toBe(screen.getByLabelText("Max turns"));

    // The ring goes after its time.
    await act(async () => {
      await vi.advanceTimersByTimeAsync(SEARCH_TARGET_MS + 50);
    });
    expect(box.classList.contains("is-search-target")).toBe(false);
  });

  test("choosing the same result again reveals it again", async () => {
    await drawer();
    typeQuery("max_turns");
    fireEvent.click(optionOf("agent:max_turns"));
    await frames.settle();
    expect(scrolledTo()).toHaveLength(1);
    const box = boxOf("agent:max_turns")!;
    const input = screen.getByLabelText("Max turns");
    expect(document.activeElement).toBe(input);

    // The cursor leaves; the field brings the results back and Enter opens
    // the one highlighted: the same one.
    act(() => input.blur());
    act(() => searchInput().focus());
    expect(optionOf("agent:max_turns").getAttribute("aria-selected")).toBe(
      "true",
    );
    fireEvent.keyDown(searchInput(), { key: "Enter" });
    await frames.settle();

    expect(scrolledTo()).toHaveLength(2);
    expect(scrolledTo()[1]).toBe(box);
    expect(box.classList.contains("is-search-target")).toBe(true);
    expect(document.activeElement).toBe(input);
  });

  test("choosing a field of a provider row opens that row and unfolds the block it stands in", async () => {
    await drawer();
    typeQuery("proxy");

    fireEvent.click(optionOf("providers:proxy", "openai"));
    await frames.settle();

    expect(window.location.hash).toBe("#/settings/providers?id=openai");
    const form = rowForm("openai")!;
    expect(form).not.toBeNull();
    expect(form.getAttribute("role")).toBe("group");
    // The block the field stands in was folded; the reveal opened it.
    const advanced = within(form).getByRole("button", {
      name: "Advanced settings",
    });
    expect(advanced.getAttribute("aria-expanded")).toBe("true");
    const box = boxOf("providers:proxy", form)!;
    expect(scrolledTo()).toContain(box);
    expect(box.classList.contains("is-search-target")).toBe(true);
    expect(document.activeElement).toBe(
      within(box).getByLabelText("Proxy URL"),
    );
  });

  test("choosing a field of another row of the open list reveals it in that row, not in the one that was open", async () => {
    await drawer({ section: "providers", item: "openai" });
    expect(rowForm("openai")).not.toBeNull();
    typeQuery("proxy");

    fireEvent.click(optionOf("providers:proxy", "anthropic"));
    await frames.settle();

    expect(window.location.hash).toBe("#/settings/providers?id=anthropic");
    expect(rowForm("openai")).toBeNull();
    const form = rowForm("anthropic")!;
    const box = boxOf("providers:proxy", form)!;
    expect(scrolledTo().at(-1)).toBe(box);
    expect(document.activeElement).toBe(
      within(box).getByLabelText("Proxy URL"),
    );
    expect(form.contains(document.activeElement)).toBe(true);
  });

  test("with two rows of one name and the second open, neither row's fields are offered", async () => {
    // The document holds two providers called "shared" and one without a name.
    await drawer({ section: "providers", item: "shared" });
    typeQuery("proxy");

    const rows = options()
      .filter((o) => o.dataset["settingsTarget"] === "providers:proxy")
      .map((o) => o.dataset["settingsRow"]);
    expect(rows).toEqual([
      "openai",
      "anthropic",
      "neuraldeep",
      "codex",
      "devin",
    ]);
    expect(options().some((o) => o.dataset["settingsRow"] === "shared")).toBe(
      false,
    );
  });

  test("the query stays after a choice, and the field brings the results back with the chosen one highlighted", async () => {
    await drawer();
    typeQuery("proxy");
    fireEvent.click(optionOf("gateways:telegram.proxy"));
    await frames.settle();
    expect(searchInput().value).toBe("proxy");
    expect(screen.queryByRole("listbox")).toBeNull();
    expect(searchInput().hasAttribute("aria-activedescendant")).toBe(false);

    act(() => searchInput().focus());

    const chosen = optionOf("gateways:telegram.proxy");
    expect(chosen.getAttribute("aria-selected")).toBe("true");
    expect(
      options().filter((o) => o.getAttribute("aria-selected") === "true"),
    ).toEqual([chosen]);
    expect(searchInput().getAttribute("aria-activedescendant")).toBe(chosen.id);

    // A rail click puts the results away and keeps the query.
    fireEvent.click(screen.getByTestId("settings-tab-agent"));
    expect(screen.queryByRole("listbox")).toBeNull();
    expect(searchInput().value).toBe("proxy");
    expect(
      screen.getByTestId("settings-tab-agent").getAttribute("aria-current"),
    ).toBe("page");
  });

  test.each([
    [
      "a fine pointer",
      "the control named by the primary name (Proxy URL's input, a model's id input), never a switch beside it, a disabled input or the (i)",
      false,
    ],
    [
      "a touch-only device",
      "the setting's named box, so no keyboard opens",
      true,
    ],
  ])("on %s the focus goes to %s", async (_pointer, _focus, touch) => {
    const config = configFromSchema(AGENT_SCHEMA);
    const providers = config["providers"] as Record<string, unknown>[];
    // A provider that goes direct: its Proxy URL input is disabled.
    providers.push({ ...providers[0]!, name: "direct", proxy: "none" });
    const cases: {
      query: string;
      anchor: string;
      row: string;
      name: string;
    }[] = [
      {
        query: "proxy",
        anchor: "providers:proxy",
        row: "openai",
        name: "Proxy URL",
      },
      {
        query: "model id",
        anchor: "models:model",
        row: "demo/qwen3-27b",
        name: "Model id",
      },
      {
        query: "proxy",
        anchor: "providers:proxy",
        row: "direct",
        name: "Proxy URL",
      },
    ];
    for (const one of cases) {
      const view = await drawer(
        touch ? { config, width: 390, touch: true } : { config },
      );
      typeQuery(one.query);
      fireEvent.click(optionOf(one.anchor, one.row));
      await frames.settle();
      const box = boxOf(one.anchor, rowForm(one.row)!)!;
      const focused = document.activeElement as HTMLElement;
      expect(scrolledTo().at(-1)).toBe(box);
      if (!touch && one.row !== "direct") {
        // The id input, not the Provider combobox before it; the URL input,
        // not the switch before it, nor the (i).
        expect(focused.tagName).toBe("INPUT");
        expect(focused.getAttribute("aria-label")).toBe(one.name);
        expect(box.contains(focused)).toBe(true);
      } else {
        // No keyboard rises: the box itself, named.
        expect(focused).toBe(box);
        expect(focused.getAttribute("role")).toBe("group");
        expect(focused.getAttribute("aria-label")).toBe(one.name);
      }
      view.unmount();
    }
  });

  test("the result's title is the primary name, whatever order the box draws its names in", async () => {
    await drawer();
    typeQuery("ignore system");
    const title = () =>
      optionOf("providers:proxy", "openai").querySelector(
        ".settings-hit-title",
      )!;
    // The word is in the second name only: it is shown beside the first.
    expect(title().textContent).toBe("Proxy URL · Ignore system proxy");

    fireEvent.click(optionOf("providers:proxy", "openai"));
    await frames.settle();
    // The box draws the switch before the URL.
    const drawn = boxOf("providers:proxy", rowForm("openai")!)!.textContent!;
    expect(drawn.indexOf("Ignore system proxy")).toBeLessThan(
      drawn.indexOf("Proxy URL"),
    );

    typeQuery("provider");
    expect(
      optionOf("models:model", "demo/qwen3-27b").querySelector(
        ".settings-hit-title",
      )!.textContent,
    ).toBe("Model id · Provider");
  });

  test("searching and choosing a result leave unsaved edits as they were", async () => {
    const view = await drawer({ section: "agent" });
    const saved = structuredClone(view.server.config);
    fireEvent.change(screen.getByLabelText("Max turns"), {
      target: { value: "45" },
    });
    const status = () => screen.getByTestId("settings-save-status").textContent;
    expect(status()).toBe("Unsaved changes");

    typeQuery("proxy");
    expect(status()).toBe("Unsaved changes");
    // The tab under the results keeps what was typed.
    expect((screen.getByLabelText("Max turns") as HTMLInputElement).value).toBe(
      "45",
    );

    fireEvent.click(optionOf("gateways:telegram.proxy"));
    await frames.settle();
    expect(status()).toBe("Unsaved changes");

    fireEvent.click(screen.getByTestId("settings-tab-agent"));
    expect((screen.getByLabelText("Max turns") as HTMLInputElement).value).toBe(
      "45",
    );
    expect(
      view.fetch.mock.calls.some(
        ([, init]) => (init as RequestInit | undefined)?.method === "PUT",
      ),
    ).toBe(false);
    expect(view.server.config).toEqual(saved);
  });

  test.each([
    "with results showing clears the query wherever the focus is",
    "in the field with the results hidden clears the query",
    "with no query takes the drawer's own step",
  ])("Escape %s", async (scenario) => {
    const closeDrawer = vi.fn();
    await drawer({
      beside: <OpenRailScreen id="settings" onClose={closeDrawer} />,
    });
    const escape = (target: Element) =>
      fireEvent.keyDown(target, { key: "Escape" });

    if (scenario.startsWith("with results showing")) {
      typeQuery("proxy");
      escape(document.body);
      expect(searchInput().value).toBe("");
      expect(screen.queryByRole("listbox")).toBeNull();

      typeQuery("proxy");
      const first = options()[0]!;
      act(() => first.focus());
      escape(first);
      expect(searchInput().value).toBe("");

      typeQuery("proxy");
      act(() => searchInput().focus());
      escape(searchInput());
      expect(searchInput().value).toBe("");
      expect(screen.queryByRole("listbox")).toBeNull();
      expect(closeDrawer).not.toHaveBeenCalled();
    } else if (scenario.startsWith("in the field")) {
      act(() => searchInput().focus());
      typeQuery("max_turns");
      fireEvent.keyDown(searchInput(), { key: "Enter" });
      expect(screen.queryByRole("listbox")).toBeNull();
      expect(searchInput().value).toBe("max_turns");
      // The cursor has not left the field yet.
      expect(document.activeElement).toBe(searchInput());
      escape(searchInput());
      expect(searchInput().value).toBe("");
      expect(closeDrawer).not.toHaveBeenCalled();
    } else {
      escape(document.body);
      expect(closeDrawer).toHaveBeenCalledTimes(1);
    }
  });

  test("the Telegram back button clears shown results first, then goes back from a tab to the tiles", async () => {
    const closeDrawer = vi.fn();
    await drawer({
      width: 390,
      touch: true,
      telegram: true,
      beside: <OpenRailScreen id="settings" onClose={closeDrawer} />,
    });
    // Telegram presses Back outside any React handler.
    const back = () => {
      let pressed: ReturnType<typeof pressBack> = "nothing";
      act(() => {
        pressed = pressBack(window, document);
      });
      return pressed;
    };
    typeQuery("max_turns");
    fireEvent.click(optionOf("agent:max_turns"));
    await frames.settle();
    expect(
      document.querySelector('input[aria-label="Max turns"]'),
    ).not.toBeNull();

    // Back from the tab: the tiles, with the query kept and its results away.
    expect(back()).toBe("escape");
    expect(screen.getByTestId("settings-tile-grid")).toBeTruthy();
    expect(searchInput().value).toBe("max_turns");
    expect(screen.queryByRole("listbox")).toBeNull();

    // Tapped open again, the results are cleared by the next Back, and the
    // tiles stay.
    fireEvent.click(searchInput());
    expect(screen.getByRole("listbox")).toBeTruthy();
    expect(screen.queryByTestId("settings-tile-grid")).toBeNull();
    expect(back()).toBe("escape");
    expect(searchInput().value).toBe("");
    expect(screen.getByTestId("settings-tile-grid")).toBeTruthy();
    expect(closeDrawer).not.toHaveBeenCalled();

    // Back again leaves Settings, as before.
    expect(back()).toBe("escape");
    expect(closeDrawer).toHaveBeenCalledTimes(1);
  });

  test.each([
    "a tip pinned by a touch, the results scrolled meanwhile, placed again",
    "an open tag editor",
    "the Skills install results",
  ])(
    "while results show, %s draws nothing in the page and claims no Escape, and comes back as it was",
    async (layer) => {
      const closeDrawer = vi.fn();
      const beside = <OpenRailScreen id="settings" onClose={closeDrawer} />;
      const escape = () => fireEvent.keyDown(document.body, { key: "Escape" });
      const clearedBy = (press: () => void) => {
        press();
        expect(searchInput().value).toBe("");
        expect(screen.queryByRole("listbox")).toBeNull();
      };

      if (layer.startsWith("a tip")) {
        // jsdom lays nothing out: give the tip a size and the (i) a place.
        const sizes = [
          vi
            .spyOn(HTMLElement.prototype, "offsetWidth", "get")
            .mockReturnValue(100),
          vi
            .spyOn(HTMLElement.prototype, "offsetHeight", "get")
            .mockReturnValue(40),
        ];
        onTestFinished(() => sizes.forEach((spy) => spy.mockRestore()));
        await drawer({ section: "agent", beside });
        const hint = screen.getAllByTestId("field-hint")[0]!;
        const place = (left: number, top: number) => {
          hint.getBoundingClientRect = () =>
            ({
              left,
              top,
              right: left + 20,
              bottom: top + 20,
              width: 20,
              height: 20,
              x: left,
              y: top,
              toJSON: () => ({}),
            }) as DOMRect;
        };
        const tip = () => screen.queryByTestId("field-hint-tip");
        place(400, 300);
        // jsdom has no PointerEvent: a plain event carries the pointer type.
        const touch = new Event("pointerdown", { bubbles: true });
        Object.defineProperty(touch, "pointerType", { value: "touch" });
        fireEvent(hint, touch);
        fireEvent.click(hint);
        expect(tip()?.style.left).toBe("360px");
        expect(tip()?.style.top).toBe("252px");

        typeQuery("proxy");
        expect(tip()).toBeNull();
        expect(document.body.querySelector('[role="tooltip"]')).toBeNull();
        // Nothing the results do closes it: not their scroll, a resize, nor a
        // press on them.
        fireEvent.scroll(screen.getByTestId("settings-search-results"));
        act(() => {
          window.dispatchEvent(new Event("resize"));
        });
        fireEvent.mouseDown(screen.getByTestId("settings-search-results"));
        // The (i) moved while the results were up.
        place(500, 100);
        clearedBy(escape);
        expect(closeDrawer).not.toHaveBeenCalled();

        // As it was: open, placed against the (i) where it is now, and still
        // pinned until something outside it is pressed or scrolled.
        expect(tip()?.textContent).toBeTruthy();
        expect(tip()?.style.left).toBe("460px");
        expect(tip()?.style.top).toBe("52px");
        fireEvent.mouseLeave(hint);
        fireEvent.blur(hint);
        expect(tip()).not.toBeNull();
        act(() => {
          document.dispatchEvent(new Event("scroll"));
        });
        expect(tip()).toBeNull();
      } else if (layer === "an open tag editor") {
        const { fetch } = await drawer({ beside });
        answering(fetch, {
          "/coddy/sessions": {
            sessions: [
              {
                id: "sess_one",
                title: "Refactor the parser",
                cwd: "/home/u/projects/coddy",
                updatedAt: "2026-09-02T10:00:00Z",
              },
            ],
            hasMore: false,
            nextCursor: null,
          },
        });
        fireEvent.click(screen.getByTestId("settings-tab-sessions_manager"));
        fireEvent.click(
          await screen.findByTestId("sessions-manager-tag-add-sess_one"),
        );
        fireEvent.change(await screen.findByTestId("session-tag-input"), {
          target: { value: "beta" },
        });

        typeQuery("proxy");
        expect(screen.queryByTestId("session-tag-editor")).toBeNull();
        expect(
          document.body.querySelector(".sessions-filter-backdrop"),
        ).toBeNull();
        clearedBy(escape);
        expect(closeDrawer).not.toHaveBeenCalled();

        // The editor is back, open, with the draft it held.
        expect(
          (screen.getByTestId("session-tag-input") as HTMLInputElement).value,
        ).toBe("beta");
        expect(
          document.body.querySelector(".sessions-filter-backdrop"),
        ).not.toBeNull();
      } else {
        await drawer({ section: "skills", beside });
        const installInput = (await screen.findByTestId(
          "skills-install-input",
        )) as HTMLInputElement;
        fireEvent.change(installInput, { target: { value: "alpha" } });
        expect(screen.getByTestId("skills-install-results")).toBeTruthy();

        typeQuery("proxy");
        // Still there, under the hidden tab, and deaf to the key.
        expect(
          screen.getByTestId("skills-install-results").closest("[hidden]"),
        ).not.toBeNull();
        clearedBy(escape);
        expect(closeDrawer).not.toHaveBeenCalled();

        // As it was: the results hang under the box, and Escape takes them
        // first again.
        expect(installInput.value).toBe("alpha");
        expect(
          screen.getByTestId("skills-install-results").closest("[hidden]"),
        ).toBeNull();
        escape();
        expect(screen.queryByTestId("skills-install-results")).toBeNull();
        expect(installInput.value).toBe("");
        expect(closeDrawer).not.toHaveBeenCalled();
      }
    },
  );

  test("the head reads Settings while results show over an open row", async () => {
    await drawer({ section: "providers", item: "openai" });
    expect(headTitle()).toBe("Provider settings");
    expect(screen.getByTestId("settings-head-back")).toBeTruthy();

    typeQuery("agent");
    expect(headTitle()).toBe("Settings");
    expect(screen.queryByTestId("settings-head-back")).toBeNull();

    typeQuery("");
    expect(headTitle()).toBe("Provider settings");
    expect(screen.getByTestId("settings-head-back")).toBeTruthy();
  });

  test("a query nothing matches says so", async () => {
    await drawer();
    typeQuery("zzzzqq");

    expect(
      screen.getByText("No matching settings; try other or fewer words"),
    ).toBeTruthy();
    expect(screen.queryByRole("listbox")).toBeNull();
    expect(searchInput().getAttribute("aria-expanded")).toBe("false");
    expect(searchInput().hasAttribute("aria-activedescendant")).toBe(false);
  });

  test("the highlighted result stays in view and stays the active descendant as the results change", async () => {
    await drawer();
    act(() => searchInput().focus());
    typeQuery("proxy");
    const input = searchInput();
    const selected = () =>
      options().filter((o) => o.getAttribute("aria-selected") === "true");
    const activeOption = () =>
      document.getElementById(input.getAttribute("aria-activedescendant")!);

    expect(selected()).toEqual([options()[0]]);
    expect(activeOption()).toBe(options()[0]);

    // The arrows move it, wrap around, and scroll it into view.
    fireEvent.keyDown(input, { key: "ArrowDown" });
    fireEvent.keyDown(input, { key: "ArrowDown" });
    expect(selected()).toEqual([options()[2]]);
    expect(activeOption()).toBe(options()[2]);
    expect(scrolled).toHaveBeenLastCalledWith({ block: "nearest" });
    expect(scrolledTo("nearest").at(-1)).toBe(options()[2]);
    fireEvent.keyDown(input, { key: "ArrowUp" });
    fireEvent.keyDown(input, { key: "ArrowUp" });
    fireEvent.keyDown(input, { key: "ArrowUp" });
    expect(selected()).toEqual([options().at(-1)]);
    expect(scrolledTo("nearest").at(-1)).toBe(options().at(-1));
    fireEvent.keyDown(input, { key: "ArrowDown" });
    expect(selected()).toEqual([options()[0]]);

    // The chosen result stays highlighted while it is among the hits ...
    fireEvent.click(optionOf("gateways:telegram.proxy"));
    await frames.settle();
    act(() => input.focus());
    expect(selected()).toEqual([optionOf("gateways:telegram.proxy")]);
    typeQuery("proxy url");
    expect(selected()).toEqual([optionOf("gateways:telegram.proxy")]);
    expect(activeOption()).toBe(optionOf("gateways:telegram.proxy"));
    expect(scrolledTo("nearest").at(-1)).toBe(
      optionOf("gateways:telegram.proxy"),
    );
    // ... and the first one takes it over when it is not.
    typeQuery("timeout");
    expect(
      options().some(
        (o) => o.dataset["settingsTarget"] === "gateways:telegram.proxy",
      ),
    ).toBe(false);
    expect(selected()).toEqual([options()[0]]);
    expect(activeOption()).toBe(options()[0]);
    // No result, no active descendant.
    typeQuery("zzzzqq");
    expect(input.hasAttribute("aria-activedescendant")).toBe(false);
  });

  test.each([
    "a Russian page finds a setting by its Russian name and by its config key",
    "a relay's settings are searched too",
  ])("%s", async (scenario) => {
    if (scenario.startsWith("a Russian")) {
      await drawer({ lang: "ru" });
      expect(searchInput().placeholder).toBe("Поиск по настройкам");
      typeQuery("max_turns");
      // The name the page draws for the setting, read from the page.
      const name = optionOf("agent:max_turns").querySelector(
        ".settings-hit-title",
      )!.textContent!;
      expect(name).toMatch(/[а-яё]/i);

      typeQuery(name);
      fireEvent.click(optionOf("agent:max_turns"));
      await frames.settle();
      expect(document.activeElement).toBe(screen.getByLabelText(name));
      expect(
        screen.getByTestId("settings-tab-agent").getAttribute("aria-current"),
      ).toBe("page");
    } else {
      await drawer({ schema: RELAY_SCHEMA });
      typeQuery("pairing");
      fireEvent.click(optionOf("swarm:pairing_tokens"));
      await frames.settle();
      expect(
        screen.getByTestId("settings-tab-swarm").getAttribute("aria-current"),
      ).toBe("page");
      const box = boxOf("swarm:pairing_tokens")!;
      expect(scrolledTo()).toContain(box);
      expect(box.classList.contains("is-search-target")).toBe(true);
      expect(box.contains(document.activeElement)).toBe(true);
    }
  });

  test("a setting hidden in a folded block is found, unfolded and then revealed, never replaced by its list's box", async () => {
    // The page the scroll happens on: nothing above the box is hidden then.
    const hiddenAtScroll: boolean[] = [];
    scrolled.mockImplementation(function (
      this: Element,
      arg?: { block?: string },
    ) {
      if (arg?.block === "center") {
        hiddenAtScroll.push(this.closest("[hidden]") !== null);
      }
    });
    await drawer();
    typeQuery("timeout_ms");

    fireEvent.click(optionOf("providers:timeout_ms", "anthropic"));
    await frames.settle();

    const form = rowForm("anthropic")!;
    expect(
      within(form)
        .getByRole("button", { name: "Advanced settings" })
        .getAttribute("aria-expanded"),
    ).toBe("true");
    const box = boxOf("providers:timeout_ms", form)!;
    expect(scrolledTo()).toEqual([box]);
    expect(hiddenAtScroll).toEqual([false]);
    expect(box.classList.contains("is-search-target")).toBe(true);
    expect(document.activeElement?.getAttribute("aria-label")).toBe(
      "Request timeout ms",
    );
    // Neither the row form nor the fold took the ring.
    expect(form.classList.contains("is-search-target")).toBe(false);
    expect(
      document.querySelector(
        ".settings-fieldset--collapsible.is-search-target",
      ),
    ).toBeNull();
  });

  test("an entry added to a list after the search was built is revealed in that entry, and with the list empty again the list is revealed", async () => {
    const config = configFromSchema(AGENT_SCHEMA);
    (config["logger"] as Record<string, unknown>)["levels"] = [
      { component: "component-0", level: "debug" },
    ];
    await drawer({ config });
    typeQuery("component");
    const listBox = () => boxOf("logger:levels")!;
    const entries = () =>
      [
        ...document.querySelectorAll<HTMLElement>("[data-settings-anchor]"),
      ].filter(
        (b) => b.dataset["settingsAnchor"] === "logger:levels.component",
      );
    const choose = async () => {
      act(() => searchInput().focus());
      fireEvent.keyDown(searchInput(), { key: "Enter" });
      await frames.settle();
    };

    fireEvent.click(optionOf("logger:levels.component"));
    await frames.settle();
    expect(entries()).toHaveLength(1);
    expect(scrolledTo().at(-1)).toBe(entries()[0]);
    expect(document.activeElement?.getAttribute("aria-label")).toBe(
      "Component",
    );

    // An entry added: the index holds no count, and the first entry in the
    // document is the one revealed.
    fireEvent.click(within(listBox()).getByRole("button", { name: "Add" }));
    expect(entries()).toHaveLength(2);
    await choose();
    expect(scrolledTo().at(-1)).toBe(entries()[0]);
    expect(entries()[0]!.contains(document.activeElement)).toBe(true);

    // Both removed: the list draws no entry, and the list itself is revealed.
    const remove = () =>
      within(listBox()).getAllByRole("button", { name: "Remove" })[0]!;
    fireEvent.click(remove());
    fireEvent.click(remove());
    expect(entries()).toHaveLength(0);
    await choose();
    expect(scrolledTo().at(-1)).toBe(listBox());
    expect(listBox().classList.contains("is-search-target")).toBe(true);
  });

  test("the number found is announced once typing pauses", async () => {
    useClockWithFrames();
    await drawer();
    const status = () =>
      within(screen.getByTestId("settings-search-results")).getByRole("status");

    typeQuery("p");
    typeQuery("pr");
    typeQuery("proxy");
    const count = options().length;
    expect(count).toBeGreaterThan(1);
    expect(status().textContent).toBe("");
    await act(async () => {
      await vi.advanceTimersByTimeAsync(450);
    });
    expect(status().textContent).toBe("");

    await act(async () => {
      await vi.advanceTimersByTimeAsync(100);
    });
    expect(status().textContent).toBe(`${count} settings found`);

    // Past the limit the rest is counted, in a line of its own, and the count
    // announced is of all that were found.
    typeQuery("e");
    expect(options()).toHaveLength(50);
    const more = Number(
      /^(\d+) more settings match - add a word to narrow the list$/.exec(
        screen.getByTestId("settings-search-more").textContent!,
      )![1],
    );
    expect(more).toBeGreaterThan(1);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(550);
    });
    expect(status().textContent).toBe(`${50 + more} settings found`);
  });
});
