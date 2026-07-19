import { useState, useEffect } from 'react';
import { useI18n } from '../contexts/I18nContext';

export default function ClientInstallationModal({ isOpen, onClose }: { isOpen: boolean, onClose: () => void }) {
  const { t } = useI18n();
  const [activeTab, setActiveTab] = useState<'macos' | 'windows' | 'linux'>('macos');
  const [copied, setCopied] = useState<string | null>(null);

  useEffect(() => {
    if (isOpen) {
      const ua = navigator.userAgent;
      if (ua.includes('Windows')) {
        setActiveTab('windows');
      } else if (ua.includes('Linux')) {
        setActiveTab('linux');
      } else {
        setActiveTab('macos');
      }
    }
  }, [isOpen]);

  const handleCopy = (text: string, id: string) => {
    navigator.clipboard.writeText(text);
    setCopied(id);
    setTimeout(() => setCopied(null), 2000);
  };

  if (!isOpen) return null;

  return (
    <div className="modal-overlay" style={{ display: 'flex', position: 'fixed', top: 0, left: 0, width: '100%', height: '100%', background: 'rgba(0,0,0,0.6)', alignItems: 'center', justifyContent: 'center', zIndex: 1000 }}>
      <div className="glass" style={{ maxWidth: '650px', width: '100%', padding: '32px', borderRadius: '16px', border: '1px solid var(--border)', textAlign: 'left', display: 'flex', flexDirection: 'column', maxHeight: '90vh' }}>
        
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '16px' }}>
          <h3 style={{ margin: 0, fontSize: '20px', display: 'flex', alignItems: 'center', gap: '8px' }}>
            💻 {t('guide_title', 'Client Installation Guide')}
          </h3>
          <button onClick={onClose} style={{ background: 'transparent', border: 'none', color: 'var(--text-muted)', cursor: 'pointer', fontSize: '24px', lineHeight: '1' }}>&times;</button>
        </div>
        
        <div style={{ fontSize: '0.9rem', color: 'var(--text-muted)', marginBottom: '20px' }}>
          {t('guide_desc', 'Choose your Operating System below to see the recommended command-line installation or direct downloads.')}
        </div>

        {/* Tab Headers */}
        <div style={{ display: 'flex', borderBottom: '1px solid var(--border)', marginBottom: '20px', gap: '16px' }}>
          {(['macos', 'windows', 'linux'] as const).map(os => (
            <button 
              key={os}
              onClick={() => setActiveTab(os)}
              style={{
                background: 'none', border: 'none', padding: '8px 16px', cursor: 'pointer', fontWeight: 500,
                color: activeTab === os ? 'var(--text)' : 'var(--text-muted)',
                borderBottom: `2px solid ${activeTab === os ? 'var(--primary)' : 'transparent'}`,
                transition: 'all 0.2s'
              }}
            >
              {t(`guide_tab_${os}`, os === 'macos' ? 'macOS' : os === 'windows' ? 'Windows' : 'Linux')}
            </button>
          ))}
        </div>

        {/* Tab Contents */}
        <div style={{ flexGrow: 1, overflowY: 'auto', paddingRight: '4px', marginBottom: '24px' }}>
          {/* macOS */}
          {activeTab === 'macos' && (
            <div className="animation-fade-in">
              <h4 style={{ fontSize: '14px', marginBottom: '8px' }}>🚀 {t('guide_macos_title', 'Apple Silicon (M1/M2/M3) & Intel Macs')}</h4>
              
              <div style={{ fontSize: '0.8rem', fontWeight: 'bold', marginTop: '10px', color: 'var(--text)' }}>
                {t('guide_macos_brew', 'Recommended via Homebrew:')}
              </div>
              <div className="code-box">
                <span>brew tap peterrichards-lr/tap && brew install lfr-tunnel</span>
                <button className="copy-btn" onClick={() => handleCopy('brew tap peterrichards-lr/tap && brew install lfr-tunnel', 'macos-brew')}>
                  {copied === 'macos-brew' ? '✓' : '📋'}
                </button>
              </div>

              <div style={{ fontSize: '0.8rem', fontWeight: 'bold', marginTop: '10px', color: 'var(--text-muted)' }}>
                {t('guide_macos_direct', 'Direct Installation Script (Alternative):')}
              </div>
              <div className="code-box">
                <span>curl -fsSL https://tunnel.lfr-demo.se/install | sh</span>
                <button className="copy-btn" onClick={() => handleCopy('curl -fsSL https://tunnel.lfr-demo.se/install | sh', 'macos-direct')}>
                  {copied === 'macos-direct' ? '✓' : '📋'}
                </button>
              </div>
              
              <div style={{ marginTop: '16px', display: 'flex', gap: '12px' }}>
                <a href="/static/downloads/lfr-tunnel-darwin-arm64" className="btn btn-outline" style={{ fontSize: '12px', padding: '6px 12px', width: 'auto', margin: 0 }}>
                  {t('guide_macos_dl_arm', 'Download arm64 (M1/M2/M3)')}
                </a>
                <a href="/static/downloads/lfr-tunnel-darwin-amd64" className="btn btn-outline" style={{ fontSize: '12px', padding: '6px 12px', width: 'auto', margin: 0 }}>
                  {t('guide_macos_dl_intel', 'Download amd64 (Intel)')}
                </a>
              </div>
            </div>
          )}

          {/* Windows */}
          {activeTab === 'windows' && (
            <div className="animation-fade-in">
              <h4 style={{ fontSize: '14px', marginBottom: '8px' }}>🚀 {t('guide_windows_title', 'Windows 10 / 11 (64-bit)')}</h4>
              
              <div style={{ fontSize: '0.8rem', fontWeight: 'bold', marginTop: '10px', color: 'var(--text)' }}>
                {t('guide_windows_scoop', 'Recommended via Scoop:')}
              </div>
              <div className="code-box">
                <span>scoop bucket add peterrichards-lr https://github.com/peterrichards-lr/scoop-bucket.git && scoop install lfr-tunnel</span>
                <button className="copy-btn" onClick={() => handleCopy('scoop bucket add peterrichards-lr https://github.com/peterrichards-lr/scoop-bucket.git && scoop install lfr-tunnel', 'win-scoop')}>
                  {copied === 'win-scoop' ? '✓' : '📋'}
                </button>
              </div>

              <div style={{ fontSize: '0.8rem', fontWeight: 'bold', marginTop: '10px', color: 'var(--text-muted)' }}>
                {t('guide_windows_direct', 'Direct Installation (PowerShell Script):')}
              </div>
              <div className="code-box">
                <span>irm https://tunnel.lfr-demo.se/install.ps1 | iex</span>
                <button className="copy-btn" onClick={() => handleCopy('irm https://tunnel.lfr-demo.se/install.ps1 | iex', 'win-direct')}>
                  {copied === 'win-direct' ? '✓' : '📋'}
                </button>
              </div>
              
              <div style={{ marginTop: '16px', display: 'flex', gap: '12px' }}>
                <a href="/static/downloads/lfr-tunnel-windows-amd64.exe" className="btn btn-outline" style={{ fontSize: '12px', padding: '6px 12px', width: 'auto', margin: 0 }}>
                  {t('guide_windows_dl', 'Download amd64 (.exe)')}
                </a>
              </div>
            </div>
          )}

          {/* Linux */}
          {activeTab === 'linux' && (
            <div className="animation-fade-in">
              <h4 style={{ fontSize: '14px', marginBottom: '8px' }}>🚀 {t('guide_linux_title', 'Linux (amd64 / arm64)')}</h4>
              
              <div style={{ fontSize: '0.8rem', fontWeight: 'bold', marginTop: '10px', color: 'var(--text)' }}>
                {t('guide_linux_direct', 'Direct Installation Script:')}
              </div>
              <div className="code-box">
                <span>curl -fsSL https://tunnel.lfr-demo.se/install | sh</span>
                <button className="copy-btn" onClick={() => handleCopy('curl -fsSL https://tunnel.lfr-demo.se/install | sh', 'linux-direct')}>
                  {copied === 'linux-direct' ? '✓' : '📋'}
                </button>
              </div>
              
              <div style={{ marginTop: '16px', display: 'flex', gap: '12px' }}>
                <a href="/static/downloads/lfr-tunnel-linux-amd64" className="btn btn-outline" style={{ fontSize: '12px', padding: '6px 12px', width: 'auto', margin: 0 }}>
                  {t('guide_linux_dl_amd', 'Download amd64')}
                </a>
                <a href="/static/downloads/lfr-tunnel-linux-arm64" className="btn btn-outline" style={{ fontSize: '12px', padding: '6px 12px', width: 'auto', margin: 0 }}>
                  {t('guide_linux_dl_arm', 'Download arm64')}
                </a>
              </div>
            </div>
          )}
        </div>

        <style>{`
          .code-box {
            margin-top: 4px;
            margin-bottom: 12px;
            position: relative;
            background: #0d1117;
            color: #e6edf3;
            border-radius: 6px;
            border: 1px solid rgba(255,255,255,0.1);
            padding: 10px 40px 10px 12px;
            font-family: monospace;
            font-size: 0.8rem;
            word-break: break-all;
          }
          .copy-btn {
            position: absolute;
            top: 6px;
            right: 6px;
            background: transparent;
            border: 1px solid rgba(255,255,255,0.2);
            color: #8b949e;
            border-radius: 4px;
            width: 22px;
            height: 22px;
            cursor: pointer;
            display: flex;
            align-items: center;
            justify-content: center;
            font-size: 10px;
          }
          .copy-btn:hover {
            background: rgba(255,255,255,0.1);
          }
          .animation-fade-in {
            animation: fadeIn 0.3s ease-out;
          }
          @keyframes fadeIn {
            from { opacity: 0; transform: translateY(5px); }
            to { opacity: 1; transform: translateY(0); }
          }
        `}</style>
      </div>
    </div>
  );
}
