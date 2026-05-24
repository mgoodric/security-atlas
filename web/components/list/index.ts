// Slice 098 — barrel export for the generic list-view shell.
//
// Five list-view slices (098/099/100/101/102) consume this from a single
// import line:
//
//   import {
//     ListPage,
//     FilterPills,
//     ListTable,
//     ListLoadingSkeleton,
//     EmptyState,
//   } from "@/components/list";
//
// Each module here is domain-agnostic — no controls/evidence/risks/etc.
// types or strings. If you need to add a domain-specific helper, put it
// in the consuming page, not here.

export { EmptyState, type EmptyStateProps } from "./empty-state";
export {
  FilterPills,
  type FilterPill,
  type FilterPillsProps,
} from "./filter-pills";
export { ListPage, type ListPageProps } from "./list-page";
export {
  ListPagination,
  paginateRows,
  paginationBounds,
  type ListPaginationProps,
} from "./pagination";
export { ListTable, type ListColumn, type ListTableProps } from "./list-table";
export {
  ListLoadingSkeleton,
  type ListLoadingSkeletonProps,
} from "./loading-skeleton";
