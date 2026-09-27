import { useEffect, useState } from 'react';
import axios from 'axios';

/**
 * The operator's never_expires policy for one resource (#2264).
 *
 * `disabled` -- the option does not appear at all.
 * `approval` -- the option appears as a REQUEST; an admin's grant is what makes it permanent.
 * `allowed`  -- the option appears plainly and is granted on the spot.
 */
export type NeverExpiresPolicy = 'disabled' | 'approval' | 'allowed';

export interface NeverExpiresPolicies {
  tokens: NeverExpiresPolicy;
  subdomains: NeverExpiresPolicy;
  custom_domains: NeverExpiresPolicy;
}

/**
 * `disabled` everywhere is the safe reading of "we do not know yet".
 *
 * A failed or in-flight /api/version must not make the portal offer something the server will
 * refuse with a 403. Offering too little is a missing option somebody reports; offering too much
 * is a button that errors, which is what #2259 was.
 */
export const NEVER_EXPIRES_UNKNOWN: NeverExpiresPolicies = {
  tokens: 'disabled',
  subdomains: 'disabled',
  custom_domains: 'disabled',
};

/**
 * Reads the never_expires policies the gateway advertises on /api/version.
 *
 * A hook rather than a prop threaded from the top, because the three places that need it --
 * token creation, the token list and the admin extension queue -- have no common ancestor that
 * already fetches this, and every other consumer of /api/version in this app fetches it where it
 * is used. One more independent fetch is cheaper than a context nobody else wants.
 *
 * The ROLE is deliberately not an input. V1 and V2 each had their own `role === 'admin'` gate,
 * they disagreed with each other for months, and neither was enforced by the server (#2259).
 * What may be granted is the operator's decision, and it arrives from the server or not at all.
 */
export function useNeverExpires(): {
  policies: NeverExpiresPolicies;
  loaded: boolean;
} {
  const [policies, setPolicies] = useState<NeverExpiresPolicies>(
    NEVER_EXPIRES_UNKNOWN,
  );
  const [loaded, setLoaded] = useState(false);

  useEffect(() => {
    let cancelled = false;
    axios
      .get('/api/version')
      .then((res) => {
        if (cancelled) return;
        const advertised = res.data?.never_expires;
        if (advertised) {
          setPolicies({
            tokens: normalise(advertised.tokens),
            subdomains: normalise(advertised.subdomains),
            custom_domains: normalise(advertised.custom_domains),
          });
        }
        setLoaded(true);
      })
      .catch(() => {
        // Left at the safe default. `loaded` stays false so a caller can tell "the gateway
        // says disabled" from "we never heard", which matters for whether to explain the
        // absence to the user or just leave the option out.
        if (!cancelled) setLoaded(false);
      });
    return () => {
      cancelled = true;
    };
  }, []);

  return { policies, loaded };
}

/**
 * Anything that is not one of the three known values reads as `disabled`.
 *
 * A gateway older than #2264 sends no never_expires block at all, and one newer than this build
 * could send a value this code has never heard of. Both must fail closed: rendering an option
 * the server will refuse is the defect, not a missing option.
 */
function normalise(value: unknown): NeverExpiresPolicy {
  return value === 'approval' || value === 'allowed' ? value : 'disabled';
}
