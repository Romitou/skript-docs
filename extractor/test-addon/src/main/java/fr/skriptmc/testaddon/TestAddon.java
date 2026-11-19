package fr.skriptmc.testaddon;

import ch.njol.skript.Skript;
import ch.njol.skript.classes.ClassInfo;
import ch.njol.skript.lang.ExpressionType;
import ch.njol.skript.lang.util.SimpleEvent;
import ch.njol.skript.registrations.Classes;
import com.destroystokyo.paper.event.player.PlayerJumpEvent;
import org.bukkit.plugin.java.JavaPlugin;

/**
 * Registers a few syntaxes the old way (Skript.register*, still what most add-ons do), each
 * covering a case of the exporter's attribution: an annotated effect, an expression without
 * documentation, an event on Skript's shared SimpleEvent class with a Paper event, and a type.
 */
@SuppressWarnings({"deprecation", "removal"})
public final class TestAddon extends JavaPlugin {

    @Override
    public void onEnable() {
        Skript.registerAddon(this);
        Classes.registerClass(new ClassInfo<>(TestThing.class, "skmctestthing")
            .user("skmc ?test ?things?")
            .name("Skmc Test Thing")
            .description("A thing of the test add-on.")
            .since("1.0"));
        Skript.registerEffect(EffTestShout.class, "skmc test shout %string%");
        Skript.registerExpression(ExprTestAnswer.class, Long.class, ExpressionType.SIMPLE, "[the] skmc test answer");
        Skript.registerEvent("Skmc test jump", SimpleEvent.class, PlayerJumpEvent.class, "skmc test jump")
            .description("When a player jumps (test add-on).")
            .since("1.1");
    }

    public static final class TestThing {
    }
}
