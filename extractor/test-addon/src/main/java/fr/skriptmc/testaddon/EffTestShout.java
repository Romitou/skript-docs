package fr.skriptmc.testaddon;

import ch.njol.skript.doc.Description;
import ch.njol.skript.doc.Examples;
import ch.njol.skript.doc.Name;
import ch.njol.skript.doc.Since;
import ch.njol.skript.lang.Effect;
import ch.njol.skript.lang.Expression;
import ch.njol.skript.lang.SkriptParser.ParseResult;
import ch.njol.util.Kleenean;
import org.bukkit.event.Event;

@Name("Skmc Test Shout")
@Description("Shouts a text (test add-on).")
@Examples("skmc test shout \"hello\"")
@Since("1.0")
public class EffTestShout extends Effect {

    @Override
    public boolean init(Expression<?>[] exprs, int matchedPattern, Kleenean isDelayed, ParseResult parseResult) {
        return true;
    }

    @Override
    protected void execute(Event event) {
    }

    @Override
    public String toString(Event event, boolean debug) {
        return "skmc test shout";
    }
}
