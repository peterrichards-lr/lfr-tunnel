export type VanityStageState = 'done' | 'open' | 'failed';

// VanityStageIcon renders the circular, white-glyph status icon spec from #964/#968: green +
// tick = timestamp set (passed), grey + dash = timestamp null (not yet reached), red + cross
// = failed at this stage. Shared between the portal's own VanityDomainStatusPanel and the
// admin-facing AdminVanityDomainStatus page so both render the exact same icon language.
export default function VanityStageIcon({
  state,
  title,
}: {
  state: VanityStageState;
  title: string;
}) {
  const style: Record<VanityStageState, { background: string; glyph: string }> =
    {
      done: { background: 'var(--success)', glyph: '✓' },
      open: { background: 'var(--text-muted)', glyph: '–' },
      failed: { background: 'var(--danger)', glyph: '✕' },
    };
  const { background, glyph } = style[state];
  return (
    <span title={title} className="stage-icon" style={{ background }}>
      {glyph}
    </span>
  );
}
