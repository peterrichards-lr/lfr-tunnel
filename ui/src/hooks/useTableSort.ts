import { useState, useMemo } from 'react';

export function useTableSort<T>(items: T[], searchKeys: (keyof T)[]) {
  const [sortConfig, setSortConfig] = useState<{ key: keyof T; direction: 'asc' | 'desc' } | null>(null);
  const [searchQuery, setSearchQuery] = useState('');

  const filteredItems = useMemo(() => {
    if (!searchQuery.trim()) return items;
    const lowerQuery = searchQuery.toLowerCase();
    
    return items.filter(item => {
      return searchKeys.some(key => {
        const val = item[key];
        if (val == null) return false;
        return String(val).toLowerCase().includes(lowerQuery);
      });
    });
  }, [items, searchQuery, searchKeys]);

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

  const requestSort = (key: keyof T) => {
    let direction: 'asc' | 'desc' = 'asc';
    if (sortConfig && sortConfig.key === key && sortConfig.direction === 'asc') {
      direction = 'desc';
    }
    setSortConfig({ key, direction });
  };

  const getSortIndicator = (key: keyof T) => {
    if (!sortConfig || sortConfig.key !== key) return null;
    return sortConfig.direction === 'asc' ? ' ↑' : ' ↓';
  };

  const getAriaSort = (key: keyof T): 'ascending' | 'descending' | 'none' => {
    if (!sortConfig || sortConfig.key !== key) return 'none';
    return sortConfig.direction === 'asc' ? 'ascending' : 'descending';
  };

  return { items: sortedItems, requestSort, getSortIndicator, searchQuery, setSearchQuery, sortConfig, getAriaSort };
}
