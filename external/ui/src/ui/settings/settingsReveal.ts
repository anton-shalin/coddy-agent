/**
 * Going to a setting the search found: the event a fold listens for and the
 * times the reveal waits and shows its mark.
 */

/**
 * Dispatched (bubbling) from a setting's box before it is scrolled to. A
 * folded block above the box opens on it, so what the operator was sent to is
 * not inside a hidden body.
 */
export const SETTINGS_REVEAL_EVENT = "coddy:settings-reveal";

/** How long the ring on a found setting stays, in milliseconds. */
export const SEARCH_TARGET_MS = 1600;

/** How long a reveal waits for its box to be drawn and shown, in milliseconds. */
export const REVEAL_WAIT_MS = 1000;
