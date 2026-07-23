import { useState } from 'react';
import { useI18n } from '../contexts/I18nContext';
import type { ColumnDef } from '../hooks/useDataTable';

interface DataTableToolbarProps<T> {
  searchQuery: string;
  onSearchChange: (query: string) => void;
  searchPlaceholder?: string;
  pageSize: number;
  onPageSizeChange: (size: number) => void;
  columns: ColumnDef<T>[];
  isColumnVisible: (key: string) => boolean;
  onToggleColumn: (key: string) => void;
  extraControls?: React.ReactNode;
}

export default function DataTableToolbar<T>({
  searchQuery,
  onSearchChange,
  searchPlaceholder,
  pageSize,
  onPageSizeChange,
  columns,
  isColumnVisible,
  onToggleColumn,
  extraControls
}: DataTableToolbarProps<T>) {
  const { t } = useI18n();
  const [isColMenuOpen, setIsColMenuOpen] = useState(false);

  return (
    <div className="flex justify-between items-center gap-md mb-lg flex-wrap">
      {/* Left side: Search & Extra Filters */}
      <div className="flex items-center gap-md flex-1 min-w-[240px]">
        <input
          type="text"
          placeholder={searchPlaceholder || t('search_placeholder', 'Search...')}
          value={searchQuery}
          onChange={e => onSearchChange(e.target.value)}
          className="search-input w-full"
        />
        {extraControls}
      </div>

      {/* Right side: Page Size & Column Selector */}
      <div className="flex items-center gap-sm">
        {/* Page Size Selector */}
        <div className="flex items-center gap-xs text-xs text-muted">
          <span>{t('tbl_page_size', 'Page Size')}:</span>
          <select
            className="input-field py-xs px-sm text-xs cursor-pointer w-auto"
            value={pageSize}
            onChange={e => onPageSizeChange(Number(e.target.value))}
          >
            <option value={10}>10</option>
            <option value={25}>25</option>
            <option value={50}>50</option>
            <option value={100}>100</option>
          </select>
        </div>

        {/* Column Visibility Selector */}
        <div className="relative inline-block">
          <button
            className="btn btn-secondary py-xs px-md text-xs flex items-center gap-xs"
            onClick={() => setIsColMenuOpen(!isColMenuOpen)}
          >
            <span>👁</span>
            <span>{t('tbl_visible_columns', 'Visible Columns')}</span>
          </button>

          {isColMenuOpen && (
            <>
              <div
                className="fixed inset-0 z-40"
                onClick={() => setIsColMenuOpen(false)}
              ></div>
              <div
                className="absolute right-0 top-full mt-xs z-50 p-md rounded-md border shadow-lg flex flex-col gap-xs min-w-[180px]"
                style={{
                  background: 'var(--bg-card-solid)',
                  borderColor: 'var(--border)'
                }}
              >
                <div className="text-2xs fw-bold text-muted uppercase tracking-wider mb-xs">
                  {t('tbl_visible_columns', 'Visible Columns')}
                </div>
                {columns.map(col => (
                  <label
                    key={col.key}
                    className="flex items-center gap-sm text-xs cursor-pointer hover:opacity-80 py-xs"
                  >
                    <input
                      type="checkbox"
                      checked={isColumnVisible(col.key)}
                      onChange={() => onToggleColumn(col.key)}
                      style={{ accentColor: 'var(--primary)' }}
                    />
                    <span>{col.label}</span>
                  </label>
                ))}
              </div>
            </>
          )}
        </div>
      </div>
    </div>
  );
}
