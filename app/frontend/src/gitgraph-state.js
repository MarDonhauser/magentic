export function branchMergeState(branch, isMain = false) {
  if (isMain) return 'not-applicable';
  if (branch?.mergedKnown !== true) return 'unknown';
  return branch?.merged ? 'merged' : 'not-merged';
}
