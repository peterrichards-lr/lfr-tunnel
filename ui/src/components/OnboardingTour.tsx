import { useEffect } from 'react';
import axios from 'axios';
import { driver } from 'driver.js';
import 'driver.js/dist/driver.css';
import { useI18n } from '../contexts/I18nContext';

export default function OnboardingTour({ user }: { user: any }) {
  const { t } = useI18n();

  useEffect(() => {
    const startTour = async (isRerun: boolean) => {
      // Report to telemetry that tour is starting
      try {
        await axios.post('/api/me/onboarding', {
          status: 'in_progress',
          last_step: 'welcome',
          is_rerun: isRerun
        });
      } catch (e) {
        console.error("Failed to report onboarding start telemetry", e);
      }

      const steps: any[] = [
        {
          element: '#dashboard-overview',
          popover: {
            title: t('tour_welcome_title', 'Welcome to the V2 Dashboard!'),
            description: t('tour_welcome_desc', "The portal has been completely rebuilt from the ground up. V2 is faster, highly secure, and features a truly premium, asynchronous user experience. Let's see what changed!"),
            side: 'bottom',
            align: 'start'
          }
        },
        {
          element: '.sidebar',
          popover: {
            title: t('tour_sidebar_title', 'Consolidated Sidebar Navigation'),
            description: t('tour_sidebar_desc', 'All features are now cleanly grouped in the sidebar. We have removed the clutter of the old V1 tabs to give you focused, dedicated views.'),
            side: 'right',
            align: 'start'
          }
        }
      ];

      if (user?.role === 'admin' || user?.role === 'owner') {
        steps.push(
          {
            element: 'a[href="/admin/users"]',
            popover: {
              title: t('tour_users_title', 'Powerful User Management'),
              description: t('tour_users_desc', 'In V2, clicking on a user opens a powerful modal. All heavy actions (Reset MFA, Set Limits, Preferred Domain) are safely packed there instead of cluttering a wide table.'),
              side: 'right',
              align: 'center'
            }
          },
          {
            element: 'a[href="/admin/subdomains"]',
            popover: {
              title: t('tour_subdomains_title', 'Advanced Lifecycle & Throttling'),
              description: t('tour_subdomains_desc', 'The Subdomains view clearly separates static reservations from active connections. You can even Throttle active connections directly from the UI without clunky browser prompts.'),
              side: 'right',
              align: 'center'
            }
          },
          {
            element: 'a[href="/admin/settings"]',
            popover: {
              title: t('tour_settings_title', 'Server-Side Safety'),
              description: t('tour_settings_desc', 'Actions like CSV/PDF exports and "Iron Curtain" lockouts now use secure Go backend streaming and custom themed modals, preventing the browser crashes we saw in V1.'),
              side: 'right',
              align: 'center'
            }
          }
        );
      }

      steps.push({
        element: '#tour-tunnels-panel',
        popover: {
          title: t('tour_end_title', 'Active Tunnels'),
          description: t('tour_end_desc', 'Your real-time tunnel connections are still right here on your Dashboard. Enjoy the new V2 experience!'),
          side: 'top',
          align: 'center'
        }
      });

      const driverObj = driver({
        showProgress: true,
        allowClose: true,
        steps: steps,
        onDestroyed: () => {
          const status = 'completed';
          axios.post('/api/me/onboarding', {
            status: status,
            last_step: 'completed',
            is_rerun: false
          }).catch(err => console.error("Failed to report onboarding completion", err));
        }
      });

      driverObj.drive();
    };

    // Auto-start if pending
    if (user && user.onboarding_status === 'pending') {
      const timer = setTimeout(() => startTour(false), 1200);
      return () => clearTimeout(timer);
    }

    // Listen for manual trigger
    const handleStartTour = () => startTour(true);
    window.addEventListener('start-onboarding-tour', handleStartTour as EventListener);

    return () => {
      window.removeEventListener('start-onboarding-tour', handleStartTour as EventListener);
    };
  }, [user, t]);

  return null; // This is a headless component
}
