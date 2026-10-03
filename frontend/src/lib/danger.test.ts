// Tests for the destructive-command detector.
//
// This module is the one place in the frontend where a bug has a physical
// consequence: a pattern that stops matching means a `rm -rf /` runs against
// production without a confirmation dialog. So the assertions below are mostly
// "this exact string is still caught", which is deliberately rigid — a
// refactor that loosens a regex should fail here rather than in the field.
//
// Run with `npm test` (node's built-in runner; no framework needed).

import { test, describe } from "node:test";
import assert from "node:assert/strict";

import { checkCommand, checkCommandAll, anyProduction } from "./danger.ts";

/** Assert a command is caught at the given level. */
function expectLevel(cmd: string, level: "warn" | "block-without-confirm") {
  const d = checkCommand(cmd);
  assert.ok(d, `expected ${JSON.stringify(cmd)} to be flagged, got null`);
  assert.equal(d.level, level, `${JSON.stringify(cmd)}: reason was "${d.reason}"`);
}

function expectClean(cmd: string) {
  const d = checkCommand(cmd);
  assert.equal(d, null, `expected ${JSON.stringify(cmd)} to be clean, got "${d?.reason}"`);
}

describe("filesystem destruction", () => {
  test("catches recursive force-delete of root and home", () => {
    for (const cmd of [
      "rm -rf /",
      "rm -fr /",
      "sudo rm -rf /",
      "rm -rf --no-preserve-root /",
      "rm -rf /*",
      "rm -rf ~",
      "rm -rf $HOME",
      "rm -rf ${HOME}",
      "rm --recursive --force /",
    ]) {
      expectLevel(cmd, "block-without-confirm");
    }
  });

  test("catches recursive delete of system directories", () => {
    for (const cmd of ["rm -rf /etc", "rm -rf /var/lib/mysql", "rm -r /usr/local"]) {
      expectLevel(cmd, "block-without-confirm");
    }
  });

  test("catches filesystem and disk erasure", () => {
    for (const cmd of [
      "mkfs.ext4 /dev/sda1",
      "mkfs -t xfs /dev/nvme0n1",
      "wipefs -a /dev/sdb",
      "blkdiscard /dev/nvme0n1",
      "sgdisk --zap-all /dev/sda",
      "dd if=/dev/zero of=/dev/sda bs=1M",
      "cat image.iso > /dev/sdb",
      "lvremove /dev/vg0/data",
      "zpool destroy tank",
      "cryptsetup luksFormat /dev/sdb1",
    ]) {
      expectLevel(cmd, "block-without-confirm");
    }
  });

  test("catches the fork bomb", () => {
    expectLevel(":(){ :|:& };:", "block-without-confirm");
  });

  test("dd to a regular file is not flagged", () => {
    expectClean("dd if=/dev/zero of=/tmp/testfile bs=1M count=10");
  });
});

describe("lockout — commands that sever your own access", () => {
  test("stopping sshd is treated as severe", () => {
    for (const cmd of [
      "systemctl stop sshd",
      "systemctl disable ssh",
      "service ssh stop",
      "systemctl mask sshd",
    ]) {
      expectLevel(cmd, "block-without-confirm");
    }
  });

  test("default-deny firewall and interface teardown are severe", () => {
    for (const cmd of [
      "iptables -P INPUT DROP",
      "nft flush ruleset",
      "ip link set eth0 down",
      "ifdown eth0",
    ]) {
      expectLevel(cmd, "block-without-confirm");
    }
  });

  test("truncating authorized_keys is severe", () => {
    for (const cmd of [
      "> ~/.ssh/authorized_keys",
      "echo '' > /root/.ssh/authorized_keys",
    ]) {
      expectLevel(cmd, "block-without-confirm");
    }
  });

  test("appending to authorized_keys is not flagged", () => {
    expectClean("cat id_rsa.pub >> ~/.ssh/authorized_keys");
  });

  test("stopping an unrelated service is only a warning", () => {
    // The generic service-stop rule must not be upgraded to the ssh severity.
    expectLevel("systemctl stop nginx", "warn");
  });
});

describe("databases", () => {
  test("catches destructive DDL", () => {
    expectLevel("psql -c 'DROP DATABASE production'", "block-without-confirm");
    expectLevel("dropdb production", "block-without-confirm");
    expectLevel("mysql -e 'DROP TABLE users'", "warn");
    expectLevel("psql -c 'TRUNCATE TABLE sessions'", "warn");
    expectLevel("redis-cli FLUSHALL", "warn");
  });

  test("flags DELETE and UPDATE without a WHERE clause", () => {
    expectLevel("psql -c 'DELETE FROM users'", "warn");
    expectLevel("mysql -e 'UPDATE users SET admin = 1'", "warn");
  });

  test("does not flag DELETE or UPDATE that are properly qualified", () => {
    expectClean("psql -c 'DELETE FROM users WHERE id = 42'");
    expectClean("mysql -e 'UPDATE users SET admin = 1 WHERE id = 42'");
  });
});

describe("orchestration and IaC", () => {
  test("catches teardown commands", () => {
    expectLevel("terraform destroy", "block-without-confirm");
    expectLevel("kubectl delete -f manifests/", "block-without-confirm");
    expectLevel("kubectl delete pods --all", "block-without-confirm");
    expectLevel("aws s3 rm s3://bucket --recursive", "block-without-confirm");
  });

  test("catches softer variants as warnings", () => {
    expectLevel("terraform apply -auto-approve", "warn");
    expectLevel("kubectl delete pod nginx-1", "warn");
    expectLevel("kubectl drain node-3", "warn");
    expectLevel("helm uninstall myapp", "warn");
    expectLevel("docker system prune -a", "warn");
  });
});

describe("version control", () => {
  test("catches force-push and discarded work", () => {
    expectLevel("git push --force origin main", "warn");
    expectLevel("git push -f", "warn");
    expectLevel("git reset --hard HEAD~3", "warn");
    expectLevel("git clean -fd", "warn");
  });

  test("--force-with-lease is not flagged", () => {
    // The safe form of force-push exists precisely so it can be used freely;
    // warning on it would train people to dismiss the dialog.
    expectClean("git push --force-with-lease origin main");
  });
});

describe("history tampering", () => {
  test("catches shell history clearing", () => {
    expectLevel("history -c", "warn");
    expectLevel("unset HISTFILE", "warn");
    expectLevel("cat /dev/null > ~/.bash_history", "warn");
  });

  test("catches log truncation", () => {
    expectLevel(": > /var/log/auth.log", "warn");
    expectLevel("cat /dev/null > /var/log/syslog", "warn");
    expectLevel("truncate -s 0 /var/log/nginx/access.log", "warn");
  });

  test("deleting /var/log is ranked as system-directory destruction", () => {
    // It matches both the history rule (warn) and the system-directory rule
    // (block). The more severe answer is the right one to surface, and this
    // pins that the ranking — not list order — decides.
    const d = checkCommand("rm -rf /var/log");
    assert.ok(d);
    assert.equal(d.level, "block-without-confirm");
    assert.ok(
      checkCommandAll("rm -rf /var/log").some((h) => h.category === "history"),
      "the history-tampering reason should still be reported",
    );
  });
});

describe("severity ranking", () => {
  test("returns the most severe match, not the first in list order", () => {
    // `systemctl stop nginx` alone is a warn and appears earlier in the list
    // than several block-level patterns; combined with mkfs the answer must be
    // the block.
    const d = checkCommand("systemctl stop nginx && mkfs.ext4 /dev/sdb1");
    assert.ok(d);
    assert.equal(d.level, "block-without-confirm");
  });

  test("checkCommandAll reports every reason, most severe first", () => {
    const all = checkCommandAll("terraform destroy && history -c");
    assert.ok(all.length >= 2, `expected multiple hits, got ${all.length}`);
    assert.equal(all[0].level, "block-without-confirm");
    assert.ok(
      all.some((d) => d.category === "history"),
      "expected the history-clearing hit to be reported too",
    );
  });

  test("every match carries a category and a non-empty matched substring", () => {
    for (const cmd of ["rm -rf /", "git push -f", "kubectl drain node-1"]) {
      for (const d of checkCommandAll(cmd)) {
        assert.ok(d.category, `${cmd}: missing category`);
        assert.ok(d.matched.length > 0, `${cmd}: empty matched text`);
        assert.ok(d.reason.length > 0, `${cmd}: empty reason`);
      }
    }
  });
});

describe("everyday commands stay clean", () => {
  test("does not flag ordinary operations", () => {
    for (const cmd of [
      "ls -la",
      "df -h",
      "systemctl status nginx",
      "systemctl restart nginx",
      "journalctl -u nginx -n 100",
      "docker ps",
      "kubectl get pods",
      "git status",
      "git pull --rebase",
      "tail -f /var/log/syslog",
      "rm /tmp/scratch.txt",
      "rm -rf ./node_modules",
      "chmod 644 config.yml",
      "terraform plan",
      "psql -c 'SELECT count(*) FROM users'",
      "apt-get update",
      "curl -sS https://example.com/health",
    ]) {
      expectClean(cmd);
    }
  });

  test("empty and whitespace input is clean", () => {
    expectClean("");
    expectClean("   \n\t ");
  });
});

describe("anyProduction", () => {
  test("detects a production host anywhere in the selection", () => {
    assert.equal(anyProduction(["dev", "staging", "production"]), true);
    assert.equal(anyProduction(["PRODUCTION"]), true, "should be case-insensitive");
    assert.equal(anyProduction(["dev", "staging"]), false);
  });

  test("tolerates null and undefined entries", () => {
    // Host.environment is optional in the bindings, so this is the shape the
    // caller actually passes.
    assert.equal(anyProduction([null, undefined, ""]), false);
    assert.equal(anyProduction([null, "production"]), true);
  });

  test("does not treat substrings as production", () => {
    assert.equal(anyProduction(["pre-production"]), false);
  });
});
