// Pane tree model for split-pane terminal layouts. A node is either a
// terminal leaf (with a sessionID) or a split container with two children.

export type Direction = "horizontal" | "vertical";

export type LeafNode = {
  kind: "leaf";
  id: string;
  sessionID: string;
};

export type SplitNode = {
  kind: "split";
  id: string;
  direction: Direction;
  a: PaneNode;
  b: PaneNode;
};

export type PaneNode = LeafNode | SplitNode;

const uuid = () =>
  typeof crypto !== "undefined" && crypto.randomUUID
    ? crypto.randomUUID()
    : Math.random().toString(36).slice(2);

export function newLeaf(): LeafNode {
  return { kind: "leaf", id: uuid(), sessionID: uuid() };
}

export function splitLeaf(
  root: PaneNode,
  leafID: string,
  direction: Direction,
): PaneNode {
  if (root.kind === "leaf") {
    if (root.id !== leafID) return root;
    return {
      kind: "split",
      id: uuid(),
      direction,
      a: root,
      b: newLeaf(),
    };
  }
  return {
    ...root,
    a: splitLeaf(root.a, leafID, direction),
    b: splitLeaf(root.b, leafID, direction),
  };
}

// closeLeaf returns the new root after removing the leaf with this id. If the
// leaf is half of a split, the sibling collapses up. If it's the only leaf
// left, return null to signal that the whole tab is gone.
export function closeLeaf(root: PaneNode, leafID: string): PaneNode | null {
  if (root.kind === "leaf") {
    return root.id === leafID ? null : root;
  }
  if (root.a.kind === "leaf" && root.a.id === leafID) return root.b;
  if (root.b.kind === "leaf" && root.b.id === leafID) return root.a;
  const newA = closeLeaf(root.a, leafID);
  const newB = closeLeaf(root.b, leafID);
  if (newA === null) return newB;
  if (newB === null) return newA;
  return { ...root, a: newA, b: newB };
}

export function leaves(root: PaneNode): LeafNode[] {
  if (root.kind === "leaf") return [root];
  return [...leaves(root.a), ...leaves(root.b)];
}
