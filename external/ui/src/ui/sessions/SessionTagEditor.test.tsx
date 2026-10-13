import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { PaneHidden } from "../components/PanePortal";
import { SessionTagEditor } from "./SessionTagEditor";

afterEach(cleanup);

// The editor is portalled to <body>, so the pane it belongs to cannot hide it
// with a style. Under a hidden pane it is not drawn; its draft and its open
// state are the host's and the component's, and come back with it.
test("an open tag editor in a hidden pane draws nothing and comes back with its draft", () => {
  const onChange = vi.fn();
  const onClose = vi.fn();
  const pane = (hidden: boolean) => (
    <PaneHidden.Provider value={hidden}>
      <SessionTagEditor
        open
        anchor={null}
        tags={["alpha"]}
        vocabulary={[]}
        onChange={onChange}
        onClose={onClose}
        ariaLabel="Chat"
      />
    </PaneHidden.Provider>
  );
  const editor = () => screen.queryByTestId("session-tag-editor");

  const { rerender } = render(pane(false));
  fireEvent.change(screen.getByTestId("session-tag-input"), {
    target: { value: "beta" },
  });
  expect(
    (screen.getByTestId("session-tag-input") as HTMLInputElement).value,
  ).toBe("beta");
  expect(editor()?.parentElement).toBe(document.body);

  rerender(pane(true));
  expect(editor()).toBeNull();
  expect(screen.queryByTestId("session-tag-input")).toBeNull();
  expect(document.body.querySelector(".sessions-filter-backdrop")).toBeNull();
  expect(document.body.textContent).not.toContain("alpha");

  rerender(pane(false));
  expect(editor()?.parentElement).toBe(document.body);
  expect(
    (screen.getByTestId("session-tag-input") as HTMLInputElement).value,
  ).toBe("beta");
  expect(
    document.body.querySelector(".sessions-filter-backdrop"),
  ).not.toBeNull();
  expect(onClose).not.toHaveBeenCalled();
  expect(onChange).not.toHaveBeenCalled();
});
