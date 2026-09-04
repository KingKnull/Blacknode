// Tests for the split-pane tree model.
//
// Worth testing carefully because the operations are recursive and immutable,
// and the failure modes are quiet: a lost sibling on close, or a mutated shared
// node, shows up as a terminal that vanishes rather than as an exception.

import { test, describe } from "node:test";
import assert from "node:assert/strict";

import {
  newLeaf,
  splitLeaf,
  setRatio,
  closeLeaf,
  leaves,
  type PaneNode,
  type SplitNode,
} from "./panes.ts";

/** Build a tree of n leaves by repeatedly splitting the first leaf. */
function treeOf(n: number): PaneNode {
  let root: PaneNode = newLeaf();
  for (let i = 1; i < n; i++) {
    root = splitLeaf(root, leaves(root)[0].id, i % 2 ? "horizontal" : "vertical");
  }
  return root;
}

function asSplit(node: PaneNode): SplitNode {
  assert.equal(node.kind, "split", "expected a split node");
  return node as SplitNode;
}

describe("newLeaf", () => {
  test("produces distinct ids and session ids", () => {
    const a = newLeaf();
    const b = newLeaf();
    assert.notEqual(a.id, b.id);
    assert.notEqual(a.sessionID, b.sessionID);
    // id and sessionID are different namespaces; conflating them would make
    // pane bookkeeping and session bookkeeping alias each other.
    assert.notEqual(a.id, a.sessionID);
  });
});

describe("splitLeaf", () => {
  test("turns the target leaf into a split with the original as child a", () => {
    const leaf = newLeaf();
    const split = asSplit(splitLeaf(leaf, leaf.id, "horizontal"));
    assert.equal(split.direction, "horizontal");
    assert.equal(split.ratio, 0.5);
    assert.equal(split.a, leaf, "the existing pane should be preserved by identity");
    assert.equal(split.b.kind, "leaf");
  });

  test("adds exactly one leaf per split, at any depth", () => {
    let root = treeOf(1);
    for (let expected = 2; expected <= 8; expected++) {
      const target = leaves(root)[leaves(root).length - 1].id;
      root = splitLeaf(root, target, "vertical");
      assert.equal(leaves(root).length, expected);
    }
  });

  test("leaf and split ids stay unique across many splits", () => {
    const root = treeOf(12);
    const ids: string[] = [];
    (function walk(n: PaneNode) {
      ids.push(n.id);
      if (n.kind === "split") {
        walk(n.a);
        walk(n.b);
      }
    })(root);
    assert.equal(new Set(ids).size, ids.length, "duplicate node id in the tree");

    const sessions = leaves(root).map((l) => l.sessionID);
    assert.equal(new Set(sessions).size, sessions.length, "duplicate sessionID");
  });

  test("an unknown leaf id leaves the set of leaves unchanged", () => {
    const root = treeOf(4);
    const before = leaves(root).map((l) => l.id);
    const after = leaves(splitLeaf(root, "no-such-id", "horizontal")).map((l) => l.id);
    assert.deepEqual(after, before);
  });

  test("does not mutate the input tree", () => {
    const root = treeOf(3);
    const snapshot = JSON.stringify(root);
    splitLeaf(root, leaves(root)[0].id, "vertical");
    assert.equal(JSON.stringify(root), snapshot);
  });
});

describe("setRatio", () => {
  test("updates the addressed split only", () => {
    const root = asSplit(treeOf(3));
    const inner = asSplit(root.a);
    const updated = asSplit(setRatio(root, inner.id, 0.3));
    assert.equal(asSplit(updated.a).ratio, 0.3);
    assert.equal(updated.ratio, root.ratio, "sibling split should be untouched");
  });

  test("clamps to a range that keeps both panes visible", () => {
    const root = asSplit(treeOf(2));
    assert.equal(asSplit(setRatio(root, root.id, 0)).ratio, 0.05);
    assert.equal(asSplit(setRatio(root, root.id, -5)).ratio, 0.05);
    assert.equal(asSplit(setRatio(root, root.id, 1)).ratio, 0.95);
    assert.equal(asSplit(setRatio(root, root.id, 99)).ratio, 0.95);
    assert.equal(asSplit(setRatio(root, root.id, 0.42)).ratio, 0.42);
  });

  test("does not mutate the input tree", () => {
    const root = asSplit(treeOf(2));
    setRatio(root, root.id, 0.2);
    assert.equal(root.ratio, 0.5);
  });
});

describe("closeLeaf", () => {
  test("collapses the sibling up when closing half of a split", () => {
    const root = asSplit(treeOf(2));
    const [first, second] = leaves(root);
    const afterClosingFirst = closeLeaf(root, first.id);
    assert.equal(afterClosingFirst?.kind, "leaf");
    assert.equal((afterClosingFirst as { id: string }).id, second.id);
  });

  test("returns null when the last leaf is closed", () => {
    const leaf = newLeaf();
    assert.equal(closeLeaf(leaf, leaf.id), null);
  });

  test("closing every leaf in turn ends at null and never loses more than one", () => {
    let root: PaneNode | null = treeOf(6);
    for (let expected = 5; expected >= 1; expected--) {
      const victim = leaves(root!)[0].id;
      root = closeLeaf(root!, victim);
      assert.ok(root, `tree vanished with ${expected} leaves still expected`);
      assert.equal(leaves(root).length, expected);
    }
    root = closeLeaf(root!, leaves(root!)[0].id);
    assert.equal(root, null);
  });

  test("closing a deeply nested leaf preserves every other session", () => {
    const root = treeOf(7);
    const all = leaves(root);
    const victim = all[3];
    const survivors = all.filter((l) => l.id !== victim.id).map((l) => l.sessionID);
    const after = closeLeaf(root, victim.id);
    assert.ok(after);
    assert.deepEqual(leaves(after).map((l) => l.sessionID).sort(), survivors.sort());
  });

  test("an unknown leaf id is a no-op", () => {
    const root = treeOf(4);
    const after = closeLeaf(root, "no-such-id");
    assert.ok(after);
    assert.deepEqual(
      leaves(after).map((l) => l.id),
      leaves(root).map((l) => l.id),
    );
  });

  test("does not mutate the input tree", () => {
    const root = treeOf(4);
    const snapshot = JSON.stringify(root);
    closeLeaf(root, leaves(root)[1].id);
    assert.equal(JSON.stringify(root), snapshot);
  });
});

describe("leaves", () => {
  test("returns leaves in left-to-right tree order", () => {
    const leafA = newLeaf();
    const split = asSplit(splitLeaf(leafA, leafA.id, "horizontal"));
    const ordered = leaves(split);
    assert.equal(ordered.length, 2);
    assert.equal(ordered[0].id, leafA.id, "child a must come first");
    assert.equal(ordered[1].id, (split.b as { id: string }).id);
  });

  test("a lone leaf is its own only leaf", () => {
    const leaf = newLeaf();
    assert.deepEqual(leaves(leaf), [leaf]);
  });
});
