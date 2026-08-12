package net.czqu.e2e.client;

import net.czqu.rmtt.client.netty.RmttClient;
import net.czqu.rmtt.client.netty.RmttClientBuilder;
import net.czqu.rmtt.protocol.ConnectReturnCode;

import java.nio.charset.StandardCharsets;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicReference;

/**
 * Java client for the cross-language E2E matrix: connects to the Go rmtt
 * server (e2e/go-server), pushes a message upstream and verifies the echo the
 * Go server sends back downstream.
 *
 * <p>Prints {@code JAVA_CLIENT_CONNECTED} / {@code JAVA_CLIENT_ECHO_OK} /
 * {@code JAVA_CLIENT_E2E_PASS} on success; exits non-zero on any failure.</p>
 */
public final class E2eJavaClientMain {

    /** Default server host when not supplied. */
    public static final String DEFAULT_HOST = "127.0.0.1";

    /** Default server port when not supplied. */
    public static final int DEFAULT_PORT = 19990;

    private E2eJavaClientMain() {
    }

    /**
     * Connect to the Go server, push upstream and verify the echo.
     *
     * @param args optional arguments: {@code [host] [port]}
     * @throws Exception when the run fails
     */
    public static void main(String[] args) throws Exception {
        String host = args.length > 0 ? args[0] : DEFAULT_HOST;
        int port = args.length > 1 ? Integer.parseInt(args[1]) : DEFAULT_PORT;

        CountDownLatch down = new CountDownLatch(1);
        AtomicReference<byte[]> got = new AtomicReference<>();
        RmttClient c = new RmttClientBuilder()
                .host(host)
                .port(port)
                .credential("java-e2e")
                .keepAliveSeconds(30)
                .connectTimeoutMillis(5000)
                .pushHandler(payload -> {
                    got.set(payload);
                    down.countDown();
                })
                .build();
        if (c.connect() != ConnectReturnCode.CONNECT_ACCEPTED) {
            System.err.println("connect rejected by Go server");
            System.exit(1);
        }
        System.out.println("JAVA_CLIENT_CONNECTED");
        c.push("ping-from-java".getBytes(StandardCharsets.UTF_8)).awaitUninterruptibly(5000);
        if (!down.await(5, TimeUnit.SECONDS)) {
            System.err.println("timeout waiting for Go server echo");
            System.exit(1);
        }
        String echo = new String(got.get(), StandardCharsets.UTF_8);
        if (!"echo:ping-from-java".equals(echo)) {
            System.err.println("unexpected echo: " + echo);
            System.exit(1);
        }
        System.out.println("JAVA_CLIENT_ECHO_OK " + echo);
        c.disconnect();
        c.shutdown();
        System.out.println("JAVA_CLIENT_E2E_PASS");
    }
}
