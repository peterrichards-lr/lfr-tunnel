import { useI18n } from '../contexts/I18nContext';

// GeoAttribution renders one vendor's credit, link and all.
//
// Every supported vendor's licence obliges a visible credit, and each obliges a DIFFERENT one:
// MaxMind's GeoLite EULA §3 ("You must provide attribution of your use to MaxMind"), DB-IP's
// CC BY 4.0 ("you must include a link back to DB-IP.com on pages that display or use results
// from the database"), IP2Location LITE's LICENSE_LITE.TXT, which prescribes its acknowledgment
// word for word, and IPinfo Lite's "IP address data is powered by IPinfo", required for
// commercial and non-commercial use alike.
//
// The gateway honours all of them to the STRICTEST standard rather than reasoning per vendor
// (#2044). Deciding per vendor is a question that would have to be answered again every time the
// vendor changed -- and since #1998 that is a SIGHUP away, so a change could open a gap nobody
// re-checked.
//
// The ANCHOR arrives as props rather than from a table here. It used to be duplicated in this
// file and in pkg/server/static/dashboard.js, and the credit now appears in the footers too --
// which would have made a third copy of a licence obligation, in a place a vendor could be added
// to one and missed in the others. pkg/geo.AttributionLink is the one table; the server sends
// href and text on /api/version (PUBLIC, so the login screen can reach it) and on the admin
// locations route.
//
// The SENTENCE stays here, as literal t() keys: scripts/check-i18n-keys.cjs can only see a
// string literal, and a key it cannot see is a key it cannot hold in the locale bundles.
//
// Renders nothing for an unset provider. That is the licence protection: with no vendor named
// there is nobody to credit, and printing any vendor's line would be a false statement about
// provenance.
export default function GeoAttribution({
  provider,
  href,
  text,
}: {
  provider?: string;
  href?: string;
  text?: string;
}) {
  const { t } = useI18n();
  if (!provider) return null;

  // One branch per selectable vendor. TestEveryPortalArmRendersACreditForEverySelectableVendor
  // fails if a vendor reaches geo.SelectableProviders without one -- which happened when IPinfo
  // was added: the key existed in all ten bundles, nothing selected it, and an IPinfo deployment
  // rendered "the vendor could not be identified" over IPinfo's data.
  const sentence =
    provider === 'maxmind'
      ? t(
          'geo_attribution_maxmind',
          'This product includes GeoLite Data created by MaxMind, available from {0}.',
        )
      : provider === 'dbip'
        ? t(
            'geo_attribution_dbip',
            '{0}, used under the Creative Commons Attribution 4.0 International licence.',
          )
        : provider === 'ip2location'
          ? t(
              'geo_attribution_ip2location',
              'Liferay Tunnel uses the IP2Location LITE database for {0}.',
            )
          : provider === 'ipinfo'
            ? t('geo_attribution_ipinfo', 'IP address data is powered by {0}.')
            : t(
                'geo_attribution_unknown',
                'The vendor of this geo-IP database could not be identified from its file metadata, so the gateway cannot render the credit line it requires. Check the licence of the file you deployed and add the attribution yourself: most geo-IP vendors require a visible one wherever their data appears.',
              );

  const [before, ...rest] = sentence.split('{0}');
  // No anchor to render: either the sentence carries no {0} (the `unknown` case, which is this
  // gateway's own words rather than a licensor's) or the server sent no link for this vendor.
  if (!rest.length || !href || !text) return <>{sentence}</>;
  return (
    <>
      {before}
      <a href={href} target="_blank" rel="noreferrer" className="text-primary">
        {text}
      </a>
      {rest.join('{0}')}
    </>
  );
}
