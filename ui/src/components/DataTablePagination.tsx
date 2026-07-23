import { useI18n } from '../contexts/I18nContext';

interface DataTablePaginationProps {
  currentPage: number;
  totalPages: number;
  totalItems: number;
  pageSize: number;
  onPageChange: (page: number) => void;
}

export default function DataTablePagination({
  currentPage,
  totalPages,
  totalItems,
  pageSize,
  onPageChange
}: DataTablePaginationProps) {
  const { t } = useI18n();

  if (totalItems === 0) return null;

  const startItem = (currentPage - 1) * pageSize + 1;
  const endItem = Math.min(totalItems, currentPage * pageSize);

  // Generate visible page numbers
  const pageNumbers: number[] = [];
  const maxPageButtons = 5;
  let startPage = Math.max(1, currentPage - Math.floor(maxPageButtons / 2));
  let endPage = startPage + maxPageButtons - 1;

  if (endPage > totalPages) {
    endPage = totalPages;
    startPage = Math.max(1, endPage - maxPageButtons + 1);
  }

  for (let i = startPage; i <= endPage; i++) {
    pageNumbers.push(i);
  }

  return (
    <div className="pagination-row flex justify-between items-center mt-lg pt-md border-t text-xs text-muted flex-wrap gap-md">
      {/* Items Summary */}
      <div>
        {t('tbl_showing_items', `Showing ${startItem} to ${endItem} of ${totalItems} items`)
          .replace('{0}', String(startItem))
          .replace('{1}', String(endItem))
          .replace('{2}', String(totalItems))}
      </div>

      {/* Page Navigation Buttons */}
      <div className="flex items-center gap-xs">
        <button
          className="btn btn-secondary py-xs px-sm text-xs"
          disabled={currentPage === 1}
          onClick={() => onPageChange(1)}
          title={t('tbl_first', 'First')}
        >
          «
        </button>
        <button
          className="btn btn-secondary py-xs px-sm text-xs"
          disabled={currentPage === 1}
          onClick={() => onPageChange(currentPage - 1)}
          title={t('tbl_prev', 'Previous')}
        >
          ‹
        </button>

        {pageNumbers.map(page => (
          <button
            key={page}
            className={`btn py-xs px-sm text-xs ${
              page === currentPage ? 'btn-primary' : 'btn-secondary'
            }`}
            onClick={() => onPageChange(page)}
          >
            {page}
          </button>
        ))}

        <button
          className="btn btn-secondary py-xs px-sm text-xs"
          disabled={currentPage === totalPages}
          onClick={() => onPageChange(currentPage + 1)}
          title={t('tbl_next', 'Next')}
        >
          ›
        </button>
        <button
          className="btn btn-secondary py-xs px-sm text-xs"
          disabled={currentPage === totalPages}
          onClick={() => onPageChange(totalPages)}
          title={t('tbl_last', 'Last')}
        >
          »
        </button>
      </div>
    </div>
  );
}
