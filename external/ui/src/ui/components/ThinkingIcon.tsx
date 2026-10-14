/**
 * The mark of a reasoning row in the transcript: a light bulb, stroked with
 * the current colour, 1em square unless the caller sizes it.
 */
export function ThinkingIcon(props: {
  className?: string;
  size?: number | string;
}) {
  const size = props.size ?? "1em";
  return (
    <svg
      className={props.className}
      viewBox="0 0 16 16"
      width={size}
      height={size}
      fill="none"
      stroke="currentColor"
      strokeWidth="1.4"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      focusable="false"
    >
      <path d="M8 1.75a4.5 4.5 0 0 0-2.7 8.1c.45.34.7.86.7 1.42v.48h4v-.48c0-.56.25-1.08.7-1.42A4.5 4.5 0 0 0 8 1.75Z" />
      <path d="M6.25 14.25h3.5" />
    </svg>
  );
}
