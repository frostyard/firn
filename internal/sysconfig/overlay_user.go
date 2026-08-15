// Ported from frostyard/snosi (GPL-3.0-only), shared/native-installer/tree/usr/libexec/snosi-install (seed_var, seed_first_user).

// First-user creation for A/B installs, reimplementing seed_first_user's
// awk-over-passwd pipeline as structured Go. The root filesystem is
// dm-verity-sealed, so the account lands where all mutable state lives:
// passwd/group/shadow/gshadow go into the /etc overlay UPPER on the var
// partition (they override the image's pristine copies wholesale — the same
// state the files reach anyway the first time systemd-sysusers or any
// user-management tool rewrites them on a booted system), and the home
// directory goes to the var filesystem's home/ (the image ships /home as a
// symlink into /var/home, bootc-style). The image's pristine /etc (uid/gid
// baseline, group membership targets, /etc/skel) is read from the ro-mounted
// root image — authoritative, not synthesized.
package sysconfig

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/frostyard/firn/internal/recipe"
)

// accountDB is one parsed passwd-style database (passwd, group, shadow,
// gshadow): colon-separated fields, one entry per line, first field the
// entry name. Lines are kept verbatim as field slices — splitting on ":"
// and rejoining is lossless for these formats — and the baseline file's
// mode bits are carried so the overlay copy matches it exactly (the fixed
// bash's `chmod --reference` behavior).
type accountDB struct {
	mode  os.FileMode
	lines [][]string
}

// loadAccountDB reads and parses path, recording its mode bits.
func loadAccountDB(path string) (*accountDB, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	db := &accountDB{mode: st.Mode().Perm()}
	text := strings.TrimSuffix(string(data), "\n")
	if text != "" {
		for _, line := range strings.Split(text, "\n") {
			db.lines = append(db.lines, strings.Split(line, ":"))
		}
	}
	return db, nil
}

// find returns the index of the entry named name, or -1.
func (db *accountDB) find(name string) int {
	for i, f := range db.lines {
		if f[0] == name {
			return i
		}
	}
	return -1
}

// addMember appends user to the member list (the LAST :-field) of an
// existing entry — append_group_member's semantics: no-op if the entry is
// absent, never duplicates a member. Works for both group and gshadow,
// whose member list is the last field in each.
func (db *accountDB) addMember(name, user string) {
	i := db.find(name)
	if i < 0 {
		return
	}
	fields := db.lines[i]
	last := fields[len(fields)-1]
	if last != "" {
		for _, m := range strings.Split(last, ",") {
			if m == user {
				return
			}
		}
		fields[len(fields)-1] = last + "," + user
	} else {
		fields[len(fields)-1] = user
	}
}

// bytes serializes the database back to file form.
func (db *accountDB) bytes() []byte {
	var b strings.Builder
	for _, f := range db.lines {
		b.WriteString(strings.Join(f, ":"))
		b.WriteByte('\n')
	}
	return []byte(b.String())
}

// ids collects the numeric id in field idx (2 for passwd uid and group
// gid) of every entry. Non-numeric values are skipped — the awk original
// silently coerced them to 0.
func (db *accountDB) ids(idx int) map[int]bool {
	out := map[int]bool{}
	for _, f := range db.lines {
		if len(f) <= idx {
			continue
		}
		if n, err := strconv.Atoi(f[idx]); err == nil {
			out[n] = true
		}
	}
	return out
}

// CreateUser creates the first user account for an A/B install: baseline
// passwd/group/shadow/gshadow are read from RootDir/etc (the pristine
// image copy; snosi mounted the erofs and read its /.etc.lower), copied
// into the overlay upper with the new account appended, and the home
// directory is built under VarDir/home from the image's /etc/skel.
//
// Supplementary groups are joined only where they exist in the baseline —
// seed_first_user's join-where-exists rule; the skipped names are returned
// for the caller to report loudly, the same contract as the deployment
// writer's CreateUser. Returns missing == nil and a nil error if u.Name is
// empty (no-op).
func (w *OverlayWriter) CreateUser(u recipe.User) (missing []string, err error) {
	if u.Name == "" {
		return nil, nil
	}
	// Deliberate convergence from fisherman (which lets useradd reject bad
	// comments late) and snosi-install (which silently replaces delimiters):
	// the shared recipe contract rejects them before either writer mutates.
	if err := recipe.ValidateFullname(u.Fullname); err != nil {
		return nil, fmt.Errorf("invalid user full name: %w", err)
	}
	// The pristine /etc baseline ships at the erofs root's top-level
	// .etc.lower (the overlay's lowerdir; snosi-install seed_first_user
	// lines 1061-1062), NOT at /etc — the image's runtime /etc is the
	// overlay itself, empty on disk.
	baseline := filepath.Join(w.RootDir, ".etc.lower")

	passwd, err := loadAccountDB(filepath.Join(baseline, "passwd"))
	if err != nil {
		return nil, fmt.Errorf("reading image passwd baseline: %w", err)
	}
	group, err := loadAccountDB(filepath.Join(baseline, "group"))
	if err != nil {
		return nil, fmt.Errorf("reading image group baseline: %w", err)
	}
	shadow, err := loadAccountDB(filepath.Join(baseline, "shadow"))
	if err != nil {
		return nil, fmt.Errorf("reading image shadow baseline: %w", err)
	}
	// gshadow is optional in the image, as in seed_first_user.
	gshadow, err := loadAccountDB(filepath.Join(baseline, "gshadow"))
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("reading image gshadow baseline: %w", err)
	}

	if passwd.find(u.Name) >= 0 {
		return nil, fmt.Errorf("username %q already exists in the image", u.Name)
	}
	// Stricter than the bash (which only checked passwd): the personal
	// group's name must be free too, or the group file would grow a
	// duplicate entry.
	if group.find(u.Name) >= 0 {
		return nil, fmt.Errorf("group %q already exists in the image", u.Name)
	}

	// First free uid/gid >= 1000, uid == gid (the image may already reserve
	// e.g. 1000). SIMPLIFIED from seed_first_user's reconciliation dance —
	// max-uid+1 from passwd, then a single awk pass over group bumping the
	// candidate on collision, then uid=gid and two "internal error"
	// re-checks (which could still die if the group file wasn't sorted).
	// Scanning one candidate at a time against BOTH id sets picks the same
	// id whenever the bash succeeded, and cannot hit its internal-error
	// path.
	uids := passwd.ids(2)
	gids := group.ids(2)
	id := 1000
	for ; id < 60000; id++ {
		if !uids[id] && !gids[id] {
			break
		}
	}
	if id >= 60000 {
		return nil, fmt.Errorf("no free uid/gid in [1000,60000) in the image baseline")
	}

	// shadow/gshadow group ownership comes from the IMAGE's numeric shadow
	// gid — the installer environment's own group database is irrelevant to
	// the installed system (seed_first_user's `install -g "${shadow_gid:-0}"`).
	shadowGid := 0
	if i := group.find("shadow"); i >= 0 && len(group.lines[i]) > 2 {
		if n, err := strconv.Atoi(group.lines[i][2]); err == nil {
			shadowGid = n
		}
	}

	hash, err := passwordHash(u)
	if err != nil {
		return nil, err
	}
	if hash == "" {
		// No password configured: lock the account (useradd's default),
		// rather than the empty field's "no password required".
		hash = "!"
	}
	lastchg := overlayNow().Unix() / 86400 // days since epoch, as the bash's $(date +%s)/86400

	// Field layouts ported from seed_first_user's printf appends. Home is
	// written as /var/home/<name> — the runtime path — where the bash wrote
	// /home/<name>; the image ships /home as a symlink to /var/home, so
	// both resolve identically on the booted system.
	idStr := strconv.Itoa(id)
	passwd.lines = append(passwd.lines, []string{u.Name, "x", idStr, idStr, u.Fullname, "/var/home/" + u.Name, "/bin/bash"})
	group.lines = append(group.lines, []string{u.Name, "x", idStr, ""})
	shadow.lines = append(shadow.lines, []string{u.Name, hash, strconv.FormatInt(lastchg, 10), "0", "99999", "7", "", "", ""})
	if gshadow != nil {
		gshadow.lines = append(gshadow.lines, []string{u.Name, "!", "", ""})
	}

	// Supplementary groups: join-where-exists against the image baseline.
	for _, g := range u.Groups {
		if group.find(g) < 0 {
			missing = append(missing, g)
			continue
		}
		group.addMember(g, u.Name)
		if gshadow != nil {
			gshadow.addMember(g, u.Name)
		}
	}

	// Write the four databases into the overlay upper, each carrying its
	// BASELINE file's mode bits (passwd/group 0644, shadow/gshadow 0640 on
	// a stock Debian image) and root/root vs root/shadow ownership. Unlike
	// the bash — which copied the files up and then edited them in place
	// per group, needing a defense-in-depth chmod reassert at the end —
	// each file is written exactly once, atomically, with the right mode
	// before the rename (see writeFileAtomic for the 2026-07-17 0600
	// /etc/group incident this structure prevents).
	upper, err := w.ensureUpper()
	if err != nil {
		return missing, err
	}
	for _, f := range []struct {
		name string
		db   *accountDB
		gid  int
	}{
		{"passwd", passwd, 0},
		{"group", group, 0},
		{"shadow", shadow, shadowGid},
		{"gshadow", gshadow, shadowGid},
	} {
		if f.db == nil { // image without gshadow
			continue
		}
		path := filepath.Join(upper, f.name)
		if err := writeFileAtomic(path, f.db.bytes(), f.db.mode); err != nil {
			return missing, err
		}
		if err := chownFn(path, 0, f.gid); err != nil {
			return missing, fmt.Errorf("chown %s: %w", path, err)
		}
	}

	// Home on the var filesystem (image /home -> /var/home), seeded from
	// the image's own /etc/skel.
	homeRoot := filepath.Join(w.VarDir, "home")
	if err := os.MkdirAll(homeRoot, 0o755); err != nil {
		return missing, fmt.Errorf("mkdir %s: %w", homeRoot, err)
	}
	home := filepath.Join(homeRoot, u.Name)
	if err := os.MkdirAll(home, 0o700); err != nil {
		return missing, fmt.Errorf("mkdir %s: %w", home, err)
	}
	skel := filepath.Join(baseline, "skel")
	if _, err := os.Stat(skel); err == nil {
		if err := copySkel(skel, home); err != nil {
			return missing, fmt.Errorf("copying skel: %w", err)
		}
	}
	if err := chownTree(home, id, id); err != nil {
		return missing, err
	}
	// The bash's `cp -a src/.` applied the source DIRECTORY's own mode to
	// the target; /etc/skel is 0755, which silently undid the intended 0700
	// (observed live 2026-07-17: created home was world-readable). copySkel
	// never touches the target root's mode, but reassert 0700 anyway as the
	// same defense in depth.
	if err := os.Chmod(home, 0o700); err != nil {
		return missing, fmt.Errorf("chmod %s: %w", home, err)
	}
	return missing, nil
}

// copySkel recursively copies the contents of src into dst, preserving each
// entry's own mode bits and symlink targets (the useful subset of `cp -a`;
// skel trees hold only files, directories, and symlinks — anything else is
// skipped). Ownership is fixed afterwards by chownTree.
func copySkel(src, dst string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		s := filepath.Join(src, e.Name())
		d := filepath.Join(dst, e.Name())
		info, err := e.Info() // lstat semantics: symlinks are not followed
		if err != nil {
			return err
		}
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			target, err := os.Readlink(s)
			if err != nil {
				return err
			}
			if err := os.Symlink(target, d); err != nil {
				return err
			}
		case info.IsDir():
			if err := os.Mkdir(d, 0o700); err != nil {
				return err
			}
			if err := copySkel(s, d); err != nil {
				return err
			}
			// Chmod explicitly: Mkdir's mode argument is masked by umask.
			if err := os.Chmod(d, info.Mode().Perm()); err != nil {
				return err
			}
		case info.Mode().IsRegular():
			data, err := os.ReadFile(s)
			if err != nil {
				return err
			}
			if err := os.WriteFile(d, data, 0o600); err != nil {
				return err
			}
			if err := os.Chmod(d, info.Mode().Perm()); err != nil {
				return err
			}
		}
	}
	return nil
}

// chownTree chowns path and everything under it to uid:gid (the bash's
// `chown -R`), via the chownFn seam.
func chownTree(path string, uid, gid int) error {
	return filepath.WalkDir(path, func(p string, _ os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := chownFn(p, uid, gid); err != nil {
			return fmt.Errorf("chown %s: %w", p, err)
		}
		return nil
	})
}

// WriteUserAuthorizedKey writes an SSH authorized_keys entry for username
// into the user's home on the var filesystem: home/<username>/.ssh/
// authorized_keys (0700 dir, 0600 file), owned by the user. The uid/gid are
// read back from the overlay upper's passwd — the database CreateUser just
// wrote, authoritative for the installed system — so call this after
// CreateUser.
//
// NEW capability vs snosi: seed_var only supported a root key (the
// sshd_config.d authorized_keys.d path); per-user keys had no installer
// support at all.
func (w *OverlayWriter) WriteUserAuthorizedKey(username, key string) error {
	if username == "" || key == "" {
		return nil
	}
	uid, gid, err := lookupUser(w.upperDir(), username)
	if err != nil {
		return err
	}
	home := filepath.Join(w.VarDir, "home", username)
	sshDir := filepath.Join(home, ".ssh")
	if err := appendAuthorizedKey(sshDir, key); err != nil {
		return err
	}
	for _, p := range []string{sshDir, filepath.Join(sshDir, "authorized_keys")} {
		if err := chownFn(p, uid, gid); err != nil {
			return fmt.Errorf("chown %s: %w", p, err)
		}
	}
	return nil
}
