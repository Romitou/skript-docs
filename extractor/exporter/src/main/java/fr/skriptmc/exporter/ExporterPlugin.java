package fr.skriptmc.exporter;

import com.google.gson.GsonBuilder;
import com.google.gson.JsonObject;
import org.bukkit.Bukkit;
import org.bukkit.event.EventHandler;
import org.bukkit.event.Listener;
import org.bukkit.event.server.ServerLoadEvent;
import org.bukkit.plugin.java.JavaPlugin;

import java.io.IOException;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.logging.Level;

/**
 * Runs once the server has finished loading (every plugin enabled, Skript done registering):
 * exports what the target add-on registered, writes the JSON file and stops the server.
 * <p>
 * Configured through the environment, set by the extractor image's entrypoint:
 * <ul>
 *     <li>{@code SKMC_EXPORT_MODE}: {@code export} (default) or {@code baseline}, which records
 *     everything registered by Skript alone (run once when the image is built).</li>
 *     <li>{@code SKMC_EXPORT_TARGET}: name of the add-on's plugin (its plugin.yml {@code name}).</li>
 *     <li>{@code SKMC_EXPORT_OUTPUT}: file to write, {@code /output/export.json} by default.</li>
 *     <li>{@code SKMC_EXPORT_BASELINE}: the baseline file, {@code /opt/skmc/baseline.json} by default.</li>
 * </ul>
 * The output always gets written, even on failure: {@code {"error": {...}}} lets the scanner report
 * why instead of a bare timeout.
 */
public final class ExporterPlugin extends JavaPlugin implements Listener {

    private boolean done;

    @Override
    public void onEnable() {
        getServer().getPluginManager().registerEvents(this, this);
    }

    @EventHandler
    public void onServerLoad(ServerLoadEvent event) {
        if (done) // reloads
            return;
        done = true;
        // One tick later: Skript finishes some registrations in tasks scheduled at startup.
        Bukkit.getScheduler().runTask(this, this::runAndStop);
    }

    private void runAndStop() {
        String mode = env("SKMC_EXPORT_MODE", "export");
        Path output = Path.of(env("SKMC_EXPORT_OUTPUT", "/output/export.json"));
        Path baseline = Path.of(env("SKMC_EXPORT_BASELINE", "/opt/skmc/baseline.json"));
        JsonObject result;
        try {
            SyntaxExporter exporter = new SyntaxExporter(getLogger());
            if (mode.equals("baseline")) {
                result = exporter.baseline();
                output = baseline;
            } else {
                result = exporter.export(env("SKMC_EXPORT_TARGET", ""), baseline);
            }
        } catch (ExportException e) {
            result = error(e.code(), e.getMessage());
        } catch (Throwable t) {
            getLogger().log(Level.SEVERE, "Syntax export failed", t);
            result = error("EXPORT_CRASHED", t.toString());
        }
        try {
            Files.createDirectories(output.toAbsolutePath().getParent());
            Files.writeString(output, new GsonBuilder().disableHtmlEscaping().setPrettyPrinting().create().toJson(result),
                StandardCharsets.UTF_8);
            getLogger().info("Wrote " + output);
        } catch (IOException e) {
            getLogger().log(Level.SEVERE, "Could not write " + output, e);
        }
        Bukkit.shutdown();
    }

    private static JsonObject error(String code, String message) {
        JsonObject error = new JsonObject();
        error.addProperty("code", code);
        error.addProperty("message", message);
        JsonObject root = new JsonObject();
        root.add("error", error);
        return root;
    }

    private static String env(String key, String fallback) {
        String value = System.getenv(key);
        return value == null || value.isBlank() ? fallback : value.trim();
    }
}
