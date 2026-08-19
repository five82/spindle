# MakeMKV Troubleshooting

Covers: MakeMKV failing to scan or rip a disc. Goal: diagnose, fix if
possible, then continue into whichever processing scenario applies (or the
normal pipeline via `spindle cache rip` + `spindle cache process`).

## 1. Gather evidence

- Reproduce cheaply: `spindle disc scan` (a scan fails faster than a rip)
  and `spindle disc scan -v` for debug output including MakeMKV's own
  messages. If the failure happened inside the daemon, `spindle logs` has
  the MakeMKV error lines.
- Run `makemkvcon -r info dev:/dev/sr0` directly when you need the raw
  message text spindle's parser may summarize away.
- Note the exact error message - MakeMKV errors are distinctive and
  searchable.
- When a rip fails with no MakeMKV error message, read the stall instead:
  pull the `rip_progress` percentages for the item out of `spindle logs`
  and look at the rate. A collapse from a steady climb to ~0%/min
  localizes an unreadable region when the error text says nothing.
- Raw-device probing (`dd if=/dev/sr0 bs=1M skip=N count=32`) confirms and
  localizes a defect, but sparse samples can never clear a disc: a defect
  narrower than the gap between samples reads as perfectly healthy. Use it
  to find damage, never to rule it out.
- Change one variable at a time. Cleaning the disc and swapping the drive
  in the same step makes the outcome unattributable either way.

## 2. Known failure classes

| Symptom | Likely cause | Fix |
|---------|--------------|-----|
| "expired"/"registration key" message | Beta key expired | Update the key in `~/.MakeMKV/settings.conf` (current beta key is published on the MakeMKV forum) |
| "this application version is too old" | Outdated MakeMKV | Update MakeMKV |
| UHD disc not recognized / "disc is likely corrupt" on a 4K disc | Drive not UHD-friendly or firmware without LibreDrive | Check `makemkvcon f -l` / LibreDrive status; this is a hardware/firmware project, not a quick fix - report it |
| `exited 0 but produced no output (saved=-1 ... errors=0)` | MakeMKV abandoned the title part-way and deleted the partial file - unreadable sectors, not a spindle bug or an empty rip | Read `stalled at N%` in the error, confirm with the `rip_progress` trend, clean the disc, then diagnose with a decrypted backup (section 3) |
| Read errors part-way through one title | Dirty/scratched disc | Clean the disc, retry; MakeMKV retries sectors itself. Persistent errors on one title: rip the others, report the bad one |
| Scan hangs for a very long time | Disc with playlist obfuscation (many fake playlists) | Let it finish (raise patience, not flags); scan once and reuse - `spindle rip` rescans, so batch your title list into one invocation |
| "failed to open disc" | Drive/udev quirk or disc not spun up | Re-insert, check `lsblk`/dmesg, try `dev:/dev/srX` explicitly |
| Title missing from scan | Below min-length filter | `spindle disc scan --min-length 0` (spindle's default for scan; the daemon uses `min_title_length` from config, default 120s) |

Web-search the exact error text plus disc title - the MakeMKV forum
(forum.makemkv.com) documents most disc-specific issues, including known
problem discs and which MakeMKV version fixed them.

## 3. Decrypted backup: diagnosis, then last resort

If direct ripping keeps failing but the drive can read the disc, a full
decrypted backup sometimes succeeds where title-mode ripping fails:

```
makemkvcon backup --decrypt disc:0 /path/to/backup
```

Run it for the diagnosis even when you expect it to fail. Backup mode
verifies M2TS files against the disc's content hash table and reports the
failing file and byte offset, turning "the rip failed" into "00234.m2ts is
unreadable at offset 14074441728". Every other stream still copies, so one
failed file among passing ones is strong evidence of localized media damage
rather than a drive, key, or configuration problem.

MakeMKV sources accept prefixed paths, and spindle passes prefixed devices
through, so the backup can then be scanned and ripped without the disc:
`spindle disc scan file:/path/to/backup` and
`spindle rip file:/path/to/backup --title N -o DIR`.

## 4. After the fix

- If the disc now reads and the content is a standard single feature:
  `spindle cache rip` + `spindle cache process` (normal pipeline).
- Otherwise continue with the matching orchestration scenario.
- A second optical drive is a genuine second opinion - different pickup,
  different firmware - and is worth one retry before giving up. But two
  drives stalling at the same offset means defective media: report it and
  stop rather than grinding further.
- Report what was wrong, what fixed it (with the source you found it from),
  and anything unresolved (e.g. one unreadable extra).
