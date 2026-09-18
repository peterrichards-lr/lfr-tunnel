import { useI18n } from '../contexts/I18nContext';

// Per-provider attribution for the geo-IP database (#1921).
//
// Every supported vendor's licence obliges a visible credit, and each obliges a DIFFERENT
// one, so this is a lookup rather than a hardcoded line: MaxMind's GeoLite EULA §3 ("You must
// provide attribution of your use to MaxMind"), DB-IP's CC BY 4.0 ("you must include a link
// back to DB-IP.com on pages that display or use results from the database"), and
// IP2Location LITE's LICENSE_LITE.TXT, which prescribes its acknowledgment word for word.
//
// The link is supplied HERE, not by the bundle: DB-IP's obligation is specifically a link, so
// a locale file that lost the anchor would quietly breach the licence. The bundle holds only
// the sentence around it, and its {0} is where the link goes.
const GEO_ATTRIBUTION_LINK: Record<string, { href: string; text: string }> = {
  maxmind: { href: 'https://www.maxmind.com', text: 'maxmind.com' },
  dbip: { href: 'https://db-ip.com', text: 'IP Geolocation by DB-IP' },
  ip2location: { href: 'https://lite.ip2location.com', text: 'IP geolocation' },
  // IPinfo Lite requires "IP address data is powered by IPinfo" for commercial AND
  // non-commercial use -- supplied by the operator from IPinfo's terms (#2008). Their Lite
  // download ships no licence file, unlike IP2Location's LICENSE_LITE.TXT, so the wording
  // could not be read from the artefact. The bundle holds the sentence and this holds the
  // anchor, so the rendered credit is theirs verbatim.
  ipinfo: { href: 'https://ipinfo.io', text: 'IPinfo' },
  // `unknown` is absent on purpose rather than mapped to a vendor: there is nobody to link
  // to, and naming a vendor anyway would be a false provenance claim AND would leave the real
  // supplier's licence unmet.
};

// GeoAttribution renders one vendor's credit, link and all.
//
// One component, two callers, on purpose (#1995). The analytics panel publishes this credit
// and System Settings previews the credit a chosen vendor WILL publish -- and a preview that
// can differ from the thing it previews is worse than no preview, because it is the screen an
// admin uses to decide what they are publishing on somebody else's behalf. Portal V1 has its
// own copy in dashboard.js for the same two surfaces; the two arms are an A/B test (#1866), so
// a credit rendered in one and not the other is a defect.
//
// Renders nothing at all for an unset provider. That is the licence protection: with no vendor
// named there is nobody to credit, and printing any vendor's line would be a false statement
// about provenance.
export default function GeoAttribution({ provider }: { provider?: string }) {
  const { t } = useI18n();
  if (!provider) return null;

  // Literal keys, one branch each, rather than t(map[provider].key):
  // scripts/check-i18n-keys.cjs can only see a string literal, and a key it cannot see is a
  // key it cannot hold in the locale bundles.
  const text =
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

  const link = GEO_ATTRIBUTION_LINK[provider];
  const [before, ...rest] = text.split('{0}');
  if (!rest.length || !link) return <>{text}</>;
  return (
    <>
      {before}
      <a
        href={link.href}
        target="_blank"
        rel="noreferrer"
        className="text-primary"
      >
        {link.text}
      </a>
      {rest.join('{0}')}
    </>
  );
}
