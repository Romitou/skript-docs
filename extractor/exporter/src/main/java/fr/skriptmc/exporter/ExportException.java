package fr.skriptmc.exporter;

/** A failure the scanner records as is, with a stable code (README § Error codes). */
final class ExportException extends Exception {

    private final String code;

    ExportException(String code, String message) {
        super(message);
        this.code = code;
    }

    String code() {
        return code;
    }
}
