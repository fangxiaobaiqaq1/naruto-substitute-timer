package android.hardware;

/** JNI signature only; the host check exclusively uses direct RGBA ByteBuffers. */
public final class HardwareBuffer {
    private HardwareBuffer() { throw new UnsupportedOperationException("host has no HardwareBuffer"); }
}
