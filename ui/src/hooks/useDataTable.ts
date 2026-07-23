import { useState, useMemo, useEffect } from 'react';

export interface ColumnDef<T> {
  key: keyof T & string;
  label: string;
  sortable?: boolean;
}

export function useDataTable<T>(
  tableId: string,
  items: T[],
  searchKeys: (keyof T)[],
  allColumns: ColumnDef<T>[],
  defaultPageSize: number = 10
) {
  // LocalStorage storage keys
  const storageKey = `lfr_table_${tableId}`;

  // Read initial stored preferences
  const storedPrefs = useMemo(() => {
    try {
      const raw = localStorage.getItem(storageKey);
      return raw ? JSON.parse(raw) : null;
    } catch {
      return null;
    }
  }, [storageKey]);

  // State
  const [searchQuery, setSearchQuery] = useState('');
  const [sortConfig, setSortConfig] = useState<{ key: keyof T; direction: 'asc' | 'desc' } | null>(
    storedPrefs?.sortConfig || null
  );
  const [pageSize, setPageSizeState] = useState<number>(storedPrefs?.pageSize || defaultPageSize);
  const [currentPage, setCurrentPage] = useState<number>(1);
  const [hiddenColumns, setHiddenColumns] = useState<string[]>(storedPrefs?.hiddenColumns || []);

  // Sync preferences to localStorage
  useEffect(() => {
    try {
      localStorage.setItem(
        storageKey,
        JSON.stringify({
          pageSize,
          sortConfig,
          hiddenColumns
        })
      );
    } catch (e) {
      console.warn('Failed to save table preferences to localStorage:', e);
    }
  }, [storageKey, pageSize, sortConfig, hiddenColumns]);

  // Filtering
  const filteredItems = useMemo(() => {
    if (!searchQuery.trim()) return items;
    const lowerQuery = searchQuery.toLowerCase();

    return items.filter(item =>
      searchKeys.some(key => {
        const val = item[key];
        if (val == null) return false;
        return String(val).toLowerCase().includes(lowerQuery);
      })
    );
  }, [items, searchQuery, searchKeys]);

  // Sorting
  const sortedItems = useMemo(() => {
    if (!sortConfig) return filteredItems;

    return [...filteredItems].sort((a, b) => {
      const aVal = a[sortConfig.key];
      const bVal = b[sortConfig.key];

      if (aVal === bVal) return 0;
      if (aVal == null) return sortConfig.direction === 'asc' ? 1 : -1;
      if (bVal == null) return sortConfig.direction === 'asc' ? -1 : 1;

      let comp = 0;
      if (typeof aVal === 'string' && typeof bVal === 'string') {
        comp = aVal.localeCompare(bVal);
      } else if (aVal < bVal) {
        comp = -1;
      } else if (aVal > bVal) {
        comp = 1;
      }

      return sortConfig.direction === 'asc' ? comp : -comp;
    });
  }, [filteredItems, sortConfig]);

  // Reset page when search or filters change
  useEffect(() => {
    setCurrentPage(1);
  }, [searchQuery, pageSize]);

  // Pagination calculations
  const totalItems = sortedItems.length;
  const totalPages = Math.max(1, Math.ceil(totalItems / pageSize));
  const safeCurrentPage = Math.min(currentPage, totalPages);

  const paginatedItems = useMemo(() => {
    const start = (safeCurrentPage - 1) * pageSize;
    return sortedItems.slice(start, start + pageSize);
  }, [sortedItems, safeCurrentPage, pageSize]);

  // Sort Handler
  const requestSort = (key: keyof T) => {
    setSortConfig(current => {
      if (!current || current.key !== key) {
        return { key, direction: 'asc' };
      }
      if (current.direction === 'asc') {
        return { key, direction: 'desc' };
      }
      return null;
    });
  };

  const getSortIndicator = (key: keyof T) => {
    if (!sortConfig || sortConfig.key !== key) return null;
    return sortConfig.direction === 'asc' ? ' ↑' : ' ↓';
  };

  const getAriaSort = (key: keyof T): 'ascending' | 'descending' | 'none' => {
    if (!sortConfig || sortConfig.key !== key) return 'none';
    return sortConfig.direction === 'asc' ? 'ascending' : 'descending';
  };

  // Column Visibility Handlers
  const toggleColumn = (columnKey: string) => {
    setHiddenColumns(current =>
      current.includes(columnKey) ? current.filter(k => k !== columnKey) : [...current, columnKey]
    );
  };

  const isColumnVisible = (columnKey: string) => !hiddenColumns.includes(columnKey);

  const setPageSize = (size: number) => {
    setPageSizeState(size);
    setCurrentPage(1);
  };

  return {
    // Data
    paginatedItems,
    sortedItems,
    filteredItems,
    totalItems,
    totalPages,
    currentPage: safeCurrentPage,
    pageSize,

    // Search & Filter
    searchQuery,
    setSearchQuery,

    // Sorting
    sortConfig,
    requestSort,
    getSortIndicator,
    getAriaSort,

    // Pagination Actions
    setCurrentPage,
    setPageSize,

    // Column Visibility
    allColumns,
    hiddenColumns,
    toggleColumn,
    isColumnVisible
  };
}
