import { useI18n } from '../contexts/I18nContext';
import { useUI } from '../contexts/UIContext';

interface SectionHeadingProps {
  /** Fragment id of the section this heading names. The element carrying it lives
      elsewhere (a <section> wrapper or the card itself), so this component links to
      the anchor rather than owning it -- an element takes one id, and several of these
      cards already spend theirs on OnboardingTour targets.

      Optional, and the controls are omitted without it: ReservationsTable takes its
      anchor as an optional prop, and a link to `#undefined` is worse than no link. */
  anchor?: string;
  /** Visible heading text, reused verbatim as the copy button's accessible name. */
  label: string;
  /** Heading level. Callers pass the level their document outline needs; this component
      has no opinion, and picking one for them would flatten the outline. */
  as?: 'h2' | 'h3' | 'h4';
  className?: string;
}

/**
 * A section heading with the two anchor controls from #1520.
 *
 * Both portals grew these independently before, one heading at a time; this exists so
 * the accessibility rules (real accessible name, aria-hidden glyph, announced copy,
 * 24x24 target) are decided once rather than re-litigated per heading.
 */
export default function SectionHeading({
  anchor,
  label,
  as: Tag = 'h3',
  className,
}: SectionHeadingProps) {
  const { t } = useI18n();
  const { showToast } = useUI();

  const href = `#${anchor}`;

  const handleCopy = async () => {
    /* Built from location rather than a stored path so this keeps working when V1/V2
       routing changes shape (#1513 proposes exactly that for V1). */
    const url = `${window.location.origin}${window.location.pathname}${window.location.search}${href}`;
    try {
      await navigator.clipboard.writeText(url);
      /* The toast is the visible half; UIContext's live regions are what make it
         audible. Copying gives no other feedback -- unlike navigating, where the URL
         changing is self-evident. */
      showToast(t('link_copied', 'Link copied'), 'success');
    } catch {
      showToast(t('link_copy_failed', 'Could not copy link'), 'error');
    }
  };

  // Nothing to link to, so this is just a heading. Rendering the controls anyway would
  // hand a keyboard user two tab stops that go nowhere.
  if (!anchor) {
    return <Tag className={className}>{label}</Tag>;
  }

  return (
    <Tag className={className}>
      <span className="heading-anchor-wrap">
        <a className="heading-anchor-link" href={href}>
          {label}
        </a>
        <button
          type="button"
          className="heading-anchor"
          /* A bare link glyph announces as "link, link emoji" and says nothing about
             what it does, so the glyph is hidden and the button carries the name. */
          aria-label={t('copy_link_to', 'Copy link to {0}').replace(
            '{0}',
            label,
          )}
          onClick={handleCopy}
        >
          <span aria-hidden="true">🔗</span>
        </button>
      </span>
    </Tag>
  );
}
