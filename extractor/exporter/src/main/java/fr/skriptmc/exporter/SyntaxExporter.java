package fr.skriptmc.exporter;

import ch.njol.skript.Skript;
import ch.njol.skript.classes.ClassInfo;
import ch.njol.skript.doc.Description;
import ch.njol.skript.doc.Documentable;
import ch.njol.skript.doc.Documentation;
import ch.njol.skript.doc.DocumentationIdProvider;
import ch.njol.skript.doc.Example;
import ch.njol.skript.doc.Examples;
import ch.njol.skript.doc.Keywords;
import ch.njol.skript.doc.Name;
import ch.njol.skript.doc.NoDoc;
import ch.njol.skript.doc.RequiredPlugins;
import ch.njol.skript.doc.Since;
import ch.njol.skript.lang.function.Function;
import ch.njol.skript.lang.function.Functions;
import ch.njol.skript.lang.function.JavaFunction;
import ch.njol.skript.registrations.Classes;
import com.google.gson.JsonArray;
import com.google.gson.JsonElement;
import com.google.gson.JsonObject;
import com.google.gson.JsonParser;
import org.bukkit.Bukkit;
import org.bukkit.event.Cancellable;
import org.bukkit.event.block.BlockCanBuildEvent;
import org.bukkit.plugin.Plugin;
import org.bukkit.plugin.java.JavaPlugin;
import org.skriptlang.skript.addon.SkriptAddon;
import org.skriptlang.skript.bukkit.registration.BukkitSyntaxInfos;
import org.skriptlang.skript.common.function.DefaultFunction;
import org.skriptlang.skript.docs.Origin;
import org.skriptlang.skript.registration.SyntaxInfo;
import org.skriptlang.skript.registration.SyntaxRegistry;

import java.io.IOException;
import java.lang.reflect.Method;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.time.Instant;
import java.util.ArrayList;
import java.util.Arrays;
import java.util.Collection;
import java.util.HashSet;
import java.util.LinkedHashSet;
import java.util.List;
import java.util.Objects;
import java.util.Set;
import java.util.TreeSet;
import java.util.logging.Logger;

/**
 * Collects the syntaxes one add-on registered.
 * <p>
 * Skript keeps every syntax in a single registry (an add-on's {@code syntaxRegistry()} is a view
 * of it), so the add-on's own elements are told apart in two steps:
 * <ol>
 *     <li>everything Skript registers on its own is subtracted: the image records it once at build
 *     time ({@link #baseline()}), with the same Paper and Skript;</li>
 *     <li>what remains is attributed with the syntax origin Skript records, and the plugin whose
 *     class loader provides the element's classes: anything from another plugin (a dependency that
 *     is itself a Skript add-on) is left out, as are Skript's own hooks, which only register when
 *     their plugin (Vault, WorldGuard...) is installed.</li>
 * </ol>
 * Elements built on a Skript class shared by every add-on ({@code SimpleEvent} for instance) have
 * no other trace of their add-on: they are kept, and counted in {@code stats.ambiguous}.
 */
final class SyntaxExporter {

    static final int FORMAT_VERSION = 1;

    /** Skript classes add-ons register directly, without subclassing them. */
    private static final Set<String> SHARED_SKRIPT_CLASSES = Set.of(
        "ch.njol.skript.lang.util.SimpleEvent",
        "ch.njol.skript.expressions.base.EventValueExpression",
        "ch.njol.skript.lang.function.SimpleJavaFunction",
        "ch.njol.skript.lang.function.JavaFunction",
        "org.skriptlang.skript.common.function.DefaultFunctionImpl"
    );

    private enum Owner { TARGET, SHARED, SKRIPT, OTHER }

    private final Logger logger;
    private final Plugin skriptPlugin = Bukkit.getPluginManager().getPlugin("Skript");
    private final List<String> warnings = new ArrayList<>();
    private Plugin target;
    private int ambiguous;
    private int skippedOther;
    private int undocumented;

    SyntaxExporter(Logger logger) {
        this.logger = logger;
    }

    // ---------------------------------------------------------------------------------------------
    // Baseline

    JsonObject baseline() {
        JsonObject root = new JsonObject();
        root.addProperty("formatVersion", FORMAT_VERSION);
        root.addProperty("skriptVersion", Skript.getVersion().toString());
        root.addProperty("minecraftVersion", Bukkit.getMinecraftVersion());
        JsonArray fingerprints = new JsonArray();
        new TreeSet<>(allFingerprints()).forEach(fingerprints::add);
        root.add("fingerprints", fingerprints);
        logger.info("Baseline: " + fingerprints.size() + " elements registered by Skript alone");
        return root;
    }

    private Set<String> allFingerprints() {
        Set<String> all = new HashSet<>();
        var registry = Skript.instance().syntaxRegistry();
        for (var key : syntaxKeys())
            for (SyntaxInfo<?> info : registry.syntaxes(key))
                all.add(fingerprint(key.name(), info));
        for (ClassInfo<?> classInfo : Classes.getClassInfos())
            all.add(fingerprint(classInfo));
        for (Function<?> function : Functions.getFunctions())
            all.add(fingerprint(function));
        return all;
    }

    private static List<SyntaxRegistry.Key<? extends SyntaxInfo<?>>> syntaxKeys() {
        return List.of(SyntaxRegistry.CONDITION, SyntaxRegistry.EFFECT, SyntaxRegistry.EXPRESSION,
            SyntaxRegistry.SECTION, SyntaxRegistry.STRUCTURE, BukkitSyntaxInfos.Event.KEY);
    }

    private static String fingerprint(String key, SyntaxInfo<?> info) {
        return key + "|" + info.type().getName() + "|" + String.join("\u0001", info.patterns());
    }

    private static String fingerprint(ClassInfo<?> classInfo) {
        return "type|" + classInfo.getCodeName();
    }

    private static String fingerprint(Function<?> function) {
        return "function|" + function.getName() + "|" + function.getSignature().toString(false, false);
    }

    // ---------------------------------------------------------------------------------------------
    // Export

    JsonObject export(String targetName, Path baselinePath) throws ExportException {
        target = findPlugin(targetName);
        if (target == null)
            throw new ExportException("TARGET_NOT_FOUND", "No plugin named '" + targetName + "' is installed");
        if (!target.isEnabled())
            throw new ExportException("TARGET_NOT_ENABLED",
                "The plugin " + target.getName() + " did not enable (missing dependency, incompatible server or Skript version?): see the server log");

        Set<String> baseline = readBaseline(baselinePath);
        var registry = Skript.instance().syntaxRegistry();

        JsonObject root = new JsonObject();
        root.addProperty("format", "skmc-syntax-export");
        root.addProperty("formatVersion", FORMAT_VERSION);
        root.addProperty("language", "en");
        root.add("source", source());

        root.add("conditions", elements(registry.syntaxes(SyntaxRegistry.CONDITION), SyntaxRegistry.CONDITION.name(), baseline));
        root.add("effects", elements(registry.syntaxes(SyntaxRegistry.EFFECT), SyntaxRegistry.EFFECT.name(), baseline));
        root.add("expressions", elements(registry.syntaxes(SyntaxRegistry.EXPRESSION), SyntaxRegistry.EXPRESSION.name(), baseline));
        root.add("sections", elements(registry.syntaxes(SyntaxRegistry.SECTION), SyntaxRegistry.SECTION.name(), baseline));
        root.add("structures", elements(registry.syntaxes(SyntaxRegistry.STRUCTURE), SyntaxRegistry.STRUCTURE.name(), baseline));
        root.add("events", events(registry.syntaxes(BukkitSyntaxInfos.Event.KEY), baseline));
        root.add("types", types(baseline));
        root.add("functions", functions(baseline));

        int total = 0;
        for (String category : List.of("conditions", "effects", "expressions", "sections", "structures", "events", "types", "functions"))
            total += root.getAsJsonArray(category).size();
        if (ambiguous > 0 && hasOtherSkriptAddons())
            warnings.add(ambiguous + " element(s) built on shared Skript classes (SimpleEvent...) were attributed to "
                + target.getName() + ", although another Skript add-on is installed as a dependency: check them.");
        if (total == 0)
            warnings.add(target.getName() + " registered no syntax: does it need a plugin that is not installed?");

        JsonObject stats = new JsonObject();
        stats.addProperty("total", total);
        stats.addProperty("ambiguous", ambiguous);
        stats.addProperty("undocumented", undocumented);
        stats.addProperty("skippedFromOtherPlugins", skippedOther);
        root.add("stats", stats);
        JsonArray warningArray = new JsonArray();
        warnings.forEach(warningArray::add);
        root.add("warnings", warningArray);
        logger.info("Exported " + total + " elements of " + target.getName());
        return root;
    }

    private static Plugin findPlugin(String name) {
        for (Plugin plugin : Bukkit.getPluginManager().getPlugins())
            if (plugin.getName().equalsIgnoreCase(name))
                return plugin;
        return null;
    }

    private Set<String> readBaseline(Path path) throws ExportException {
        try {
            JsonObject root = JsonParser.parseString(Files.readString(path, StandardCharsets.UTF_8)).getAsJsonObject();
            String recordedFor = root.get("skriptVersion").getAsString();
            if (!recordedFor.equals(Skript.getVersion().toString()))
                throw new ExportException("BASELINE_MISMATCH",
                    "The baseline was recorded with Skript " + recordedFor + ", not " + Skript.getVersion());
            Set<String> fingerprints = new HashSet<>();
            for (JsonElement element : root.getAsJsonArray("fingerprints"))
                fingerprints.add(element.getAsString());
            return fingerprints;
        } catch (IOException | RuntimeException e) {
            throw new ExportException("BASELINE_MISSING", "Could not read the baseline " + path + ": " + e);
        }
    }

    private JsonObject source() {
        JsonObject source = new JsonObject();
        source.addProperty("plugin", target.getName());
        source.addProperty("version", target.getPluginMeta().getVersion());
        source.addProperty("skriptVersion", Skript.getVersion().toString());
        source.addProperty("minecraftVersion", Bukkit.getMinecraftVersion());
        source.addProperty("server", Bukkit.getName() + " " + Bukkit.getVersion());
        source.addProperty("generatedAt", Instant.now().toString());
        JsonArray plugins = new JsonArray();
        for (Plugin plugin : Bukkit.getPluginManager().getPlugins())
            if (!plugin.getName().equals("SkmcSyntaxExporter"))
                plugins.add(plugin.getName() + " " + plugin.getPluginMeta().getVersion() + (plugin.isEnabled() ? "" : " (disabled)"));
        source.add("installedPlugins", plugins);
        return source;
    }

    private boolean hasOtherSkriptAddons() {
        for (Plugin plugin : Bukkit.getPluginManager().getPlugins()) {
            if (plugin == target || plugin == skriptPlugin || plugin.getName().equals("SkmcSyntaxExporter"))
                continue;
            if (plugin.getPluginMeta().getPluginDependencies().contains("Skript")
                || plugin.getPluginMeta().getPluginSoftDependencies().contains("Skript"))
                return true;
        }
        return false;
    }

    // ---------------------------------------------------------------------------------------------
    // Attribution

    private Owner owner(Origin origin, Class<?>... classes) {
        Owner fromOrigin = null;
        if (origin instanceof Origin.AddonOrigin addonOrigin) {
            SkriptAddon addon = addonOrigin.addon();
            if (addon.name().equalsIgnoreCase(target.getName()) || providedBy(addon.source()) == target)
                return Owner.TARGET;
            if (!addon.name().equalsIgnoreCase("Skript"))
                fromOrigin = Owner.OTHER;
        }
        boolean shared = false;
        boolean other = fromOrigin == Owner.OTHER;
        for (Class<?> c : classes) {
            if (c == null)
                continue;
            Plugin plugin = providedBy(c);
            if (plugin == target)
                return Owner.TARGET;
            if (plugin != null && plugin != skriptPlugin)
                other = true;
            if (SHARED_SKRIPT_CLASSES.contains(c.getName()))
                shared = true;
        }
        if (other)
            return Owner.OTHER;
        return shared ? Owner.SHARED : Owner.SKRIPT;
    }

    private static Plugin providedBy(Class<?> c) {
        if (c == null)
            return null;
        try {
            return JavaPlugin.getProvidingPlugin(c);
        } catch (IllegalArgumentException | IllegalStateException e) { // JDK, Bukkit or server class
            return null;
        }
    }

    /** True when the element belongs to the target, counting the ambiguous ones. */
    private boolean keep(Owner owner) {
        switch (owner) {
            case TARGET:
                return true;
            case SHARED:
                ambiguous++;
                return true;
            case OTHER:
                skippedOther++;
                return false;
            default:
                return false;
        }
    }

    // ---------------------------------------------------------------------------------------------
    // Elements

    private JsonArray elements(Collection<? extends SyntaxInfo<?>> infos, String key, Set<String> baseline) {
        JsonArray array = new JsonArray();
        for (SyntaxInfo<?> info : infos) {
            if (info instanceof BukkitSyntaxInfos.Event<?>) // structures include events, exported separately
                continue;
            if (baseline.contains(fingerprint(key, info)))
                continue;
            if (!keep(owner(info.origin(), info.type())))
                continue;
            JsonObject element = annotatedElement(info);
            if (element != null)
                array.add(element);
        }
        return array;
    }

    private JsonObject annotatedElement(SyntaxInfo<?> info) {
        Class<?> type = info.type();
        if (type.isAnnotationPresent(NoDoc.class))
            return null;
        JsonObject json = new JsonObject();
        json.addProperty("id", safeId(() -> DocumentationIdProvider.getId(info), type.getSimpleName()));
        Name name = type.getAnnotation(Name.class);
        if (name == null || name.value().isBlank())
            undocumented++;
        json.addProperty("name", name != null && !name.value().isBlank() ? name.value() : humanize(type.getSimpleName()));
        json.addProperty("documented", name != null);
        json.add("description", array(type.getAnnotation(Description.class) == null ? null : type.getAnnotation(Description.class).value()));
        json.add("patterns", patterns(info.patterns()));
        json.add("examples", array(examples(type)));
        json.add("since", array(type.getAnnotation(Since.class) == null ? null : type.getAnnotation(Since.class).value()));
        json.add("requiredPlugins", array(type.getAnnotation(RequiredPlugins.class) == null ? null : type.getAnnotation(RequiredPlugins.class).value()));
        json.add("keywords", array(type.getAnnotation(Keywords.class) == null ? null : type.getAnnotation(Keywords.class).value()));
        json.addProperty("deprecated", type.isAnnotationPresent(Deprecated.class));
        if (info instanceof SyntaxInfo.Expression<?, ?> expression)
            json.addProperty("returnType", typeName(expression.returnType()));
        return json;
    }

    private static String[] examples(Class<?> type) {
        List<String> examples = new ArrayList<>();
        Examples legacy = type.getAnnotation(Examples.class);
        if (legacy != null)
            examples.addAll(Arrays.asList(legacy.value()));
        Example.Examples container = type.getAnnotation(Example.Examples.class);
        if (container != null)
            for (Example example : container.value())
                examples.add(example.value());
        Example single = type.getAnnotation(Example.class);
        if (single != null)
            examples.add(single.value());
        return examples.toArray(new String[0]);
    }

    private JsonArray events(Collection<BukkitSyntaxInfos.Event<?>> infos, Set<String> baseline) {
        JsonArray array = new JsonArray();
        for (BukkitSyntaxInfos.Event<?> info : infos) {
            if (baseline.contains(fingerprint(BukkitSyntaxInfos.Event.KEY.name(), info)))
                continue;
            List<Class<?>> classes = new ArrayList<>();
            classes.add(info.type());
            classes.addAll(info.events());
            if (!keep(owner(info.origin(), classes.toArray(new Class<?>[0]))))
                continue;
            if (info.description().size() == 1 && Objects.equals(info.description().getFirst(), "*No documentation*"))
                continue; // SkriptEventInfo.NO_DOC
            JsonObject json = new JsonObject();
            json.addProperty("id", safeId(() -> DocumentationIdProvider.getId(info), info.id()));
            json.addProperty("name", info.name());
            json.addProperty("documented", !info.description().isEmpty());
            if (info.description().isEmpty())
                undocumented++;
            json.add("description", array(info.description().toArray(new String[0])));
            json.add("patterns", patterns(info.patterns()));
            json.add("examples", array(info.examples().toArray(new String[0])));
            json.add("since", array(info.since().toArray(new String[0])));
            json.add("requiredPlugins", array(info.requiredPlugins().toArray(new String[0])));
            json.add("keywords", array(info.keywords().toArray(new String[0])));
            boolean cancellable = false;
            for (Class<?> event : info.events())
                if (Cancellable.class.isAssignableFrom(event) || BlockCanBuildEvent.class.isAssignableFrom(event))
                    cancellable = true;
            json.addProperty("cancellable", cancellable);
            array.add(json);
        }
        return array;
    }

    private JsonArray types(Set<String> baseline) {
        JsonArray array = new JsonArray();
        for (ClassInfo<?> info : Classes.getClassInfos()) {
            if (baseline.contains(fingerprint(info)))
                continue;
            Class<?> defaultExpression = info.getDefaultExpression() == null ? null : info.getDefaultExpression().getClass();
            Owner owner = owner(null, info.getC(),
                info.getParser() == null ? null : info.getParser().getClass(),
                info.getSerializer() == null ? null : info.getSerializer().getClass(),
                info.getChanger() == null ? null : info.getChanger().getClass(),
                defaultExpression);
            if (owner != Owner.TARGET) { // a type has no shared Skript class to be built on
                if (owner == Owner.OTHER)
                    skippedOther++;
                continue;
            }
            if (!info.hasDocs())
                continue;
            JsonObject json = new JsonObject();
            json.addProperty("id", safeId(() -> DocumentationIdProvider.getId(info), info.getCodeName()));
            json.addProperty("name", Objects.requireNonNullElse(info.getDocName(), info.getCodeName()));
            json.addProperty("documented", info.getDescription() != null);
            json.add("description", array(info.getDescription()));
            String[] usage = info.getUsage();
            json.add("patterns", usage == null || usage.length == 0 ? array(new String[]{info.getCodeName()}) : patterns(Arrays.asList(usage)));
            json.add("examples", array(info.getExamples()));
            json.add("since", array(info.getSince() == null ? null : new String[]{info.getSince()}));
            json.add("requiredPlugins", array(info.getRequiredPlugins()));
            json.addProperty("codeName", info.getCodeName());
            array.add(json);
        }
        return array;
    }

    private JsonArray functions(Set<String> baseline) {
        JsonArray array = new JsonArray();
        for (Function<?> function : Functions.getFunctions()) {
            if (baseline.contains(fingerprint(function)))
                continue;
            Owner owner;
            if (function instanceof DefaultFunction<?> defaultFunction) {
                SkriptAddon source = defaultFunction.source();
                owner = owner(Origin.of(source), function.getClass());
            } else {
                owner = owner(null, function.getClass());
            }
            if (!keep(owner))
                continue;
            JsonObject json = new JsonObject();
            json.addProperty("id", safeId(() -> DocumentationIdProvider.getId(function), "function_" + function.getName()));
            json.addProperty("name", function.getName());
            List<String> description = List.of();
            List<String> examples = List.of();
            List<String> since = List.of();
            List<String> keywords = List.of();
            if (function instanceof Documentable documentable) {
                description = documentable.description();
                examples = documentable.examples();
                since = documentable.since();
                keywords = documentable.keywords();
            } else if (function instanceof JavaFunction<?> javaFunction) {
                description = list(javaFunction.getDescription());
                examples = list(javaFunction.getExamples());
                since = javaFunction.getSince() == null ? List.of() : List.of(javaFunction.getSince());
                keywords = list(javaFunction.getKeywords());
            }
            json.addProperty("documented", !description.isEmpty());
            if (description.isEmpty())
                undocumented++;
            json.add("description", array(description.toArray(new String[0])));
            json.add("patterns", array(new String[]{function.getSignature().toString(false, false)}));
            json.add("examples", array(examples.toArray(new String[0])));
            json.add("since", array(since.toArray(new String[0])));
            json.add("keywords", array(keywords.toArray(new String[0])));
            ClassInfo<?> returnType = function.getReturnType();
            if (returnType != null)
                json.addProperty("returnType", Objects.requireNonNullElse(returnType.getDocName(), returnType.getCodeName()));
            array.add(json);
        }
        return array;
    }

    // ---------------------------------------------------------------------------------------------
    // Helpers

    private String safeId(java.util.function.Supplier<String> provider, String fallback) {
        try {
            return provider.get();
        } catch (RuntimeException e) {
            warnings.add("No documentation ID for " + fallback + " (" + e + "), the class name is used instead");
            return fallback;
        }
    }

    private static String typeName(Class<?> type) {
        try {
            ClassInfo<?> info = Classes.getSuperClassInfo(type);
            String name = info.getDocName();
            if (name == null || name.equals(ClassInfo.NO_DOC))
                name = info.getName().getSingular();
            return name;
        } catch (RuntimeException e) {
            return type.getSimpleName();
        }
    }

    /** "ExprMongoKeys" → "Mongo Keys": the name of an element without a {@code @Name}. */
    static String humanize(String className) {
        String name = className.replaceFirst("^(Expr|Eff|Cond|Sec|Evt|Struct|Event|Effect|Condition|Expression|Section)(?=[A-Z])", "");
        name = name.replaceAll("([a-z0-9])([A-Z])", "$1 $2").replaceAll("([A-Z]+)([A-Z][a-z])", "$1 $2");
        return name.isBlank() ? className : name.trim();
    }

    private static List<String> list(String[] values) {
        return values == null ? List.of() : Arrays.asList(values);
    }

    private static JsonArray array(String[] values) {
        JsonArray array = new JsonArray();
        if (values != null)
            for (String value : values)
                if (value != null && !value.isBlank())
                    array.add(value);
        return array;
    }

    private static final Method CLEAN_PATTERNS;

    static {
        Method method = null;
        try {
            method = Documentation.class.getDeclaredMethod("cleanPatterns", String.class, boolean.class, boolean.class);
            method.setAccessible(true);
        } catch (ReflectiveOperationException | RuntimeException ignored) {
        }
        CLEAN_PATTERNS = method;
    }

    /** The patterns as Skript's documentation shows them (no parse marks or tags). */
    private static JsonArray patterns(Collection<String> patterns) {
        Set<String> cleaned = new LinkedHashSet<>();
        for (String pattern : patterns) {
            String value = pattern;
            if (CLEAN_PATTERNS != null) {
                try {
                    value = (String) CLEAN_PATTERNS.invoke(null, pattern, false, false);
                } catch (ReflectiveOperationException | RuntimeException ignored) {
                }
            }
            cleaned.add(value.trim());
        }
        return array(cleaned.toArray(new String[0]));
    }

}
