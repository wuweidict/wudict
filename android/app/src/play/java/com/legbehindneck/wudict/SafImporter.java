// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Copies a user-picked folder out of the Storage Access Framework and into the
// app's own dictionary folder, where the server can open it as ordinary paths
// (D62).
//
// It mirrors the WHOLE picked subtree rather than filtering by extension, and
// that is not laziness. Every format here is a bundle whose parts are found by
// path arithmetic in the Go code — .mdx beside its .mdd, StarDict's
// .ifo/.idx/.dict.dz/.syn, a DSL beside its _abrv and its res/ folder of
// media (internal/format/stardict/stardict.go:153,402 and
// internal/format/dsl/dsl.go:121). Relative layout has to survive the copy
// exactly, and the user picking the folder IS the statement of what to bring.
package com.legbehindneck.wudict;

import android.app.Activity;
import android.app.AlertDialog;
import android.content.ContentResolver;
import android.content.Context;
import android.database.Cursor;
import android.net.Uri;
import android.os.StatFs;
import android.provider.DocumentsContract;
import android.provider.OpenableColumns;
import android.util.Log;

import java.io.File;
import java.io.FileOutputStream;
import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import java.net.HttpURLConnection;
import java.net.URL;
import java.util.ArrayDeque;
import java.util.ArrayList;
import java.util.Deque;
import java.util.List;
import java.util.Locale;

final class SafImporter {

    private static final String TAG = "wudict";

    private static final int BUF = 1 << 20;            // 1 MiB: these are multi-GB files
    private static final long SLACK = 64L << 20;       // leave the volume some room
    private static final int MAX_DEPTH = 32;           // a provider is not required to be sane
    private static final int MAX_ENTRIES = 100_000;

    // One import at a time. Two concurrent copies into the same destination
    // would race on the same .part files.
    private static volatile boolean running;
    private static volatile boolean cancelled;

    private SafImporter() {
    }

    /** One entry to copy: where to read it from, and where it goes relative to the destination root. */
    private static final class Item {
        final Uri src;
        final String rel;   // sanitised, '/'-separated, never absolute or escaping
        final long size;    // -1 when the provider does not say

        Item(Uri src, String rel, long size) {
            this.src = src;
            this.rel = rel;
            this.size = size;
        }
    }

    // ── entry points ─────────────────────────────────────────────────────

    static void importTree(Activity a, Uri tree) {
        if (!claim(a)) return;
        run(a, () -> {
            String rootId = DocumentsContract.getTreeDocumentId(tree);
            String folder = safeName(displayName(a.getContentResolver(),
                    DocumentsContract.buildDocumentUriUsingTree(tree, rootId)));
            List<Item> items = new ArrayList<>();
            enumerate(a.getContentResolver(), tree, rootId, "", items);
            // folder is where the bytes land; source is what the DELETE OFFER
            // names, and it stays null when the provider would not tell us, so
            // that offer never invents a folder name the user cannot find.
            return new Batch(folder == null ? "Imported" : folder, folder, items, true);
        });
    }

    /** Share-target route: a flat set of documents, no tree grant, no folder name. */
    static void importDocuments(Activity a, List<Uri> docs) {
        if (!claim(a)) return;
        run(a, () -> {
            ContentResolver cr = a.getContentResolver();
            List<Item> items = new ArrayList<>();
            for (Uri u : docs) {
                String name = safeName(displayName(cr, u));
                if (name == null) continue;
                items.add(new Item(u, name, sizeOf(cr, u)));
            }
            return new Batch("Imported", null, items, false);
        });
    }

    // ── the work ─────────────────────────────────────────────────────────

    private static final class Batch {
        final String folder;     // DESTINATION subfolder under appDicts — ours, always non-null
        final String source;     // what the user's folder is CALLED on their device, or null
        final List<Item> items;
        final boolean deletable; // a tree grant may allow removing the user's own files

        Batch(String folder, String source, List<Item> items, boolean deletable) {
            this.folder = folder;
            this.source = source;
            this.items = items;
            this.deletable = deletable;
        }
    }

    private interface Plan {
        Batch build() throws Exception;
    }

    private static boolean claim(Activity a) {
        if (running) {
            toastDialog(a, a.getString(R.string.import_busy));
            return false;
        }
        running = true;
        cancelled = false;
        return true;
    }

    private static void run(Activity a, Plan plan) {
        AlertDialog dialog = new AlertDialog.Builder(a)
                .setTitle(R.string.import_title)
                .setMessage(a.getString(R.string.import_scanning))
                .setCancelable(false)
                .setNegativeButton(R.string.import_cancel, (d, w) -> cancelled = true)
                .create();
        dialog.show();

        Thread t = new Thread(() -> {
            String summary;
            try {
                summary = copy(a, plan.build(), dialog);
            } catch (Exception e) {
                Log.w(TAG, "import failed", e);
                summary = a.getString(R.string.import_failed, String.valueOf(e.getMessage()));
            } finally {
                running = false;
            }
            final String msg = summary;
            a.runOnUiThread(() -> {
                if (a.isFinishing() || a.isDestroyed()) return;
                dialog.dismiss();
                toastDialog(a, msg);
            });
        }, "wudict-import");
        t.setDaemon(true);
        t.start();
    }

    private static String copy(Activity a, Batch batch, AlertDialog dialog) {
        if (batch.items.isEmpty()) return a.getString(R.string.import_nothing);

        File root = new File(AppDirs.appDicts(a), batch.folder);
        sweepPartials(root);
        // Only what is NOT already here counts against free space. Summing the
        // whole batch made a re-import of a folder that is 95 % imported —
        // the normal way a user adds one dictionary — refuse itself with "not
        // enough space" for bytes it was never going to write.
        long need = 0;
        for (Item it : batch.items) {
            if (it.size <= 0) continue;
            File dst = resolve(root, it.rel);
            if (dst != null && dst.isFile() && dst.length() == it.size) continue;
            need += it.size;
        }
        long free = new StatFs(AppDirs.appDicts(a).getAbsolutePath()).getAvailableBytes();
        if (need + SLACK > free) {
            return a.getString(R.string.import_no_space, size(need), size(free));
        }

        ContentResolver cr = a.getContentResolver();
        int done = 0, skipped = 0;
        List<Item> copied = new ArrayList<>();
        List<String> failed = new ArrayList<>();

        for (Item it : batch.items) {
            if (cancelled) break;
            File dst = resolve(root, it.rel);
            if (dst == null) {
                failed.add(it.rel);
                continue;
            }
            // Already there at the same size: a re-import to pick up newly
            // added dictionaries must not re-copy gigabytes it already has.
            if (dst.isFile() && it.size >= 0 && dst.length() == it.size) {
                skipped++;
                copied.add(it);
                continue;
            }
            final int n = done + skipped + failed.size() + 1;
            final int total = batch.items.size();
            a.runOnUiThread(() -> {
                if (!a.isFinishing() && !a.isDestroyed()) {
                    dialog.setMessage(a.getString(R.string.import_progress, n, total, it.rel));
                }
            });
            if (copyOne(cr, it.src, dst)) {
                done++;
                copied.add(it);
            } else {
                failed.add(it.rel);
            }
        }

        rescan();
        a.runOnUiThread(() -> {
            if (!a.isFinishing() && !a.isDestroyed() && a instanceof MainActivity) {
                ((MainActivity) a).reloadPage(); // the panel is showing a stale library
            }
        });

        if (cancelled) {
            return a.getString(R.string.import_cancelled, done);
        }
        if (batch.deletable && !copied.isEmpty() && failed.isEmpty()) {
            offerDelete(a, cr, batch.source, copied, done + skipped);
            return null; // offerDelete owns the closing message
        }
        if (!failed.isEmpty()) {
            return a.getString(R.string.import_partial, done + skipped, failed.size());
        }
        return a.getString(R.string.import_done, done + skipped);
    }

    // Writes to <name>.part and renames on success, so an interrupted import
    // never leaves a truncated file that discovery would open as a dictionary
    // and a re-run resumes cleanly.
    private static boolean copyOne(ContentResolver cr, Uri src, File dst) {
        File parent = dst.getParentFile();
        if (parent != null && !parent.isDirectory() && !parent.mkdirs()) {
            Log.w(TAG, "cannot create " + parent);
            return false;
        }
        File part = new File(dst.getPath() + ".part");
        try (InputStream in = cr.openInputStream(src);
             OutputStream out = new FileOutputStream(part)) {
            if (in == null) return false;
            byte[] buf = new byte[BUF];
            for (int n; (n = in.read(buf)) > 0; ) {
                if (cancelled) {
                    part.delete();
                    return false;
                }
                out.write(buf, 0, n);
            }
        } catch (IOException | SecurityException | IllegalArgumentException e) {
            Log.w(TAG, "copy failed: " + src, e);
            part.delete();
            return false;
        }
        if (dst.exists() && !dst.delete()) {
            part.delete();
            return false;
        }
        if (!part.renameTo(dst)) {
            part.delete();
            return false;
        }
        return true;
    }

    // These are the USER'S OWN FILES on shared storage, not our temporaries —
    // the copy this import just made is the app's permanent library and the
    // only bytes the server can ever read (D62). So removal is offered, never
    // done; taking it up turns the import into a move, which is what keeps a
    // multi-GB collection from existing twice.
    //
    // Two rules the wording has to satisfy, both learned from the shipped
    // version being unanswerable ("keep or delete SOURCES — which files?").
    // FIRST, the question names the folder as the user's own device shows it
    // and states what deleting frees, because "sources" is our word and a
    // number of gigabytes is the only part of this the user actually cares
    // about. SECOND, KEEP sits in the affirmative slot: the destructive answer
    // must not be where a reflex tap lands, and the cost of keeping is one
    // folder the user can delete later, while the cost of a mistaken delete is
    // their collection.
    private static void offerDelete(Activity a, ContentResolver cr, String source,
                                    List<Item> docs, int count) {
        long known = 0;
        for (Item it : docs) {
            if (it.size > 0) known += it.size;
        }
        final long freed = known;
        a.runOnUiThread(() -> {
            if (a.isFinishing() || a.isDestroyed()) return;
            String where = source != null
                    // “…” as escapes: javac's source encoding is a
                    // build setting, not something this file may assume.
                    ? "\u201C" + source + "\u201D"
                    : a.getString(R.string.import_source_fallback);
            // A provider is not obliged to report sizes. Promising to free
            // "0 KB" would be worse than promising nothing.
            String msg = freed > 0
                    ? a.getString(R.string.import_done_offer_delete, count, where, size(freed))
                    : a.getString(R.string.import_done_offer_delete_nosize, count, where);
            new AlertDialog.Builder(a)
                    .setTitle(R.string.import_title)
                    .setMessage(msg)
                    .setPositiveButton(R.string.import_keep_files, null)
                    .setNegativeButton(R.string.import_delete_files, (d, w) -> {
                        Thread t = new Thread(() -> {
                            int gone = 0;
                            long back = 0;
                            for (Item it : docs) {
                                try {
                                    if (DocumentsContract.deleteDocument(cr, it.src)) {
                                        gone++;
                                        if (it.size > 0) back += it.size;
                                    }
                                } catch (Exception e) {
                                    Log.w(TAG, "could not delete " + it.src, e);
                                }
                            }
                            final int n = gone;
                            final long got = back;
                            a.runOnUiThread(() -> {
                                if (!a.isFinishing() && !a.isDestroyed()) {
                                    toastDialog(a, a.getString(R.string.import_deleted, n, size(got)));
                                }
                            });
                        }, "wudict-import-delete");
                        t.setDaemon(true);
                        t.start();
                    })
                    .show();
        });
    }

    // Our own leftovers, and the one thing here that IS deleted without asking.
    // A kill mid-copy strands a <name>.part that nothing else will ever remove,
    // that discovery ignores, and that the user can only see as unexplained app
    // storage. They are bytes we wrote for an import that did not finish, and a
    // re-run rewrites them; claim() guarantees no concurrent import is holding
    // one open.
    private static void sweepPartials(File root) {
        if (root == null || !root.isDirectory()) return;
        Deque<File> stack = new ArrayDeque<>();
        stack.push(root);
        for (int seen = 0; !stack.isEmpty() && seen < MAX_ENTRIES; seen++) {
            File[] kids = stack.pop().listFiles();
            if (kids == null) continue; // unreadable is not fatal: it is a sweep
            for (File f : kids) {
                if (f.isDirectory()) {
                    stack.push(f);
                } else if (f.getName().endsWith(".part") && !f.delete()) {
                    Log.w(TAG, "stale partial left behind: " + f);
                }
            }
        }
    }

    // ── SAF plumbing ─────────────────────────────────────────────────────

    // Breadth-first rather than recursive: a provider is free to report a
    // directory tree that is deep, cyclic, or both, and a stack overflow in a
    // background thread is a crash, not an error message.
    private static void enumerate(ContentResolver cr, Uri tree, String rootId,
                                 String rootRel, List<Item> out) {
        Deque<String[]> queue = new ArrayDeque<>(); // {docId, relPath, depth}
        queue.add(new String[]{rootId, rootRel, "0"});
        while (!queue.isEmpty() && !cancelled && out.size() < MAX_ENTRIES) {
            String[] cur = queue.poll();
            int depth = Integer.parseInt(cur[2]);
            if (depth > MAX_DEPTH) continue;
            Uri children = DocumentsContract.buildChildDocumentsUriUsingTree(tree, cur[0]);
            try (Cursor c = cr.query(children, new String[]{
                    DocumentsContract.Document.COLUMN_DOCUMENT_ID,
                    DocumentsContract.Document.COLUMN_DISPLAY_NAME,
                    DocumentsContract.Document.COLUMN_MIME_TYPE,
                    DocumentsContract.Document.COLUMN_SIZE,
            }, null, null, null)) {
                if (c == null) continue;
                while (c.moveToNext() && !cancelled && out.size() < MAX_ENTRIES) {
                    String id = c.getString(0);
                    String name = safeName(c.getString(1));
                    String mime = c.getString(2);
                    if (id == null || name == null) continue;
                    String rel = cur[1].isEmpty() ? name : cur[1] + "/" + name;
                    if (DocumentsContract.Document.MIME_TYPE_DIR.equals(mime)) {
                        queue.add(new String[]{id, rel, String.valueOf(depth + 1)});
                    } else {
                        long size = c.isNull(3) ? -1 : c.getLong(3);
                        out.add(new Item(
                                DocumentsContract.buildDocumentUriUsingTree(tree, id), rel, size));
                    }
                }
            } catch (Exception e) {
                Log.w(TAG, "cannot list " + cur[0], e); // one unreadable folder is not fatal
            }
        }
    }

    private static String displayName(ContentResolver cr, Uri uri) {
        try (Cursor c = cr.query(uri, new String[]{OpenableColumns.DISPLAY_NAME},
                null, null, null)) {
            if (c != null && c.moveToFirst() && !c.isNull(0)) return c.getString(0);
        } catch (Exception e) {
            Log.w(TAG, "no display name for " + uri, e);
        }
        String last = uri.getLastPathSegment();
        if (last == null) return null;
        int cut = last.lastIndexOf('/');
        return cut >= 0 ? last.substring(cut + 1) : last;
    }

    private static long sizeOf(ContentResolver cr, Uri uri) {
        try (Cursor c = cr.query(uri, new String[]{OpenableColumns.SIZE}, null, null, null)) {
            if (c != null && c.moveToFirst() && !c.isNull(0)) return c.getLong(0);
        } catch (Exception e) {
            Log.w(TAG, "no size for " + uri, e);
        }
        return -1;
    }

    // ── hostile-name defence ─────────────────────────────────────────────

    // A DISPLAY_NAME comes from a provider, which is another app. Anything
    // that could climb out of the destination is rejected outright rather than
    // rewritten: a dictionary whose parts were silently renamed would fail
    // later, in the Go code, as a missing sibling.
    private static String safeName(String raw) {
        if (raw == null) return null;
        String n = raw.trim();
        if (n.isEmpty() || n.equals(".") || n.equals("..")) return null;
        if (n.indexOf('/') >= 0 || n.indexOf('\\') >= 0 || n.indexOf('\0') >= 0) return null;
        return n;
    }

    // Second line of defence: whatever the segments were, the resulting path
    // must still be inside the destination root.
    private static File resolve(File root, String rel) {
        File dst = new File(root, rel);
        try {
            String base = root.getCanonicalPath() + File.separator;
            if (!dst.getCanonicalPath().startsWith(base)) {
                Log.w(TAG, "refusing to write outside " + root + ": " + rel);
                return null;
            }
        } catch (IOException e) {
            return null;
        }
        return dst;
    }

    // ── after ────────────────────────────────────────────────────────────

    // The server already exposes a rescan (internal/server/server.go:116);
    // nothing new is added to its API for the port's sake.
    private static void rescan() {
        HttpURLConnection c = null;
        try {
            c = (HttpURLConnection) new URL("http://" + ServerProcess.HOST + ":"
                    + ServerProcess.PORT + "/api/rescan").openConnection();
            c.setConnectTimeout(2000);
            c.setReadTimeout(120_000); // a rescan of a fresh import indexes headwords
            c.getResponseCode();
        } catch (Exception e) {
            Log.w(TAG, "rescan after import failed", e); // the next launch picks it up
        } finally {
            if (c != null) c.disconnect();
        }
    }

    private static void toastDialog(Context c, String message) {
        if (message == null) return;
        new AlertDialog.Builder(c)
                .setMessage(message)
                .setPositiveButton(android.R.string.ok, null)
                .show();
    }

    // A dictionary collection is measured in gigabytes, and "12800 MB" is a
    // number the reader has to convert before it means anything.
    private static String size(long bytes) {
        if (bytes >= 1L << 30) {
            return String.format(Locale.US, "%.1f GB", bytes / (double) (1L << 30));
        }
        if (bytes >= 1L << 20) return (bytes >> 20) + " MB";
        return Math.max(bytes, 0) / 1024 + " KB";
    }
}
