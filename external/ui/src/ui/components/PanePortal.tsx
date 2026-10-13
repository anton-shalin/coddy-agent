import { createContext, useContext, type ReactNode } from "react";
import { createPortal } from "react-dom";

/**
 * True for the subtree of a pane that is mounted but hidden under another view
 * (Settings keeps its tab under the search results). A hidden pane is deaf: a
 * layer in it draws nothing into the document and claims no Escape, and its
 * state stays where it is until the pane is shown again.
 *
 * Two shared pieces read it: PanePortal and useEscapeCloses. A portal that
 * does not go through PanePortal, or a document listener that does not go
 * through useEscapeCloses, is not covered.
 */
export const PaneHidden = createContext(false);

/**
 * createPortal(children, document.body) for a layer that lives in a pane, and
 * nothing while the pane is hidden. A portal escapes `display: none` on the
 * pane, so the pane cannot hide it with a style.
 */
export function PanePortal(props: { children: ReactNode }) {
  const hidden = useContext(PaneHidden);
  return hidden ? null : createPortal(props.children, document.body);
}
