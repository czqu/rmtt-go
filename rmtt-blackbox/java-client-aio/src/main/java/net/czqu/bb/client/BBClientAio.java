package net.czqu.bb.client;

import net.czqu.rmtt.client.aio.RmttClient;
import net.czqu.rmtt.client.aio.RmttClientBuilder;
import net.czqu.rmtt.protocol.ConnectReturnCode;

import javax.net.ssl.SSLContext;
import javax.net.ssl.TrustManager;
import javax.net.ssl.X509TrustManager;
import java.nio.charset.StandardCharsets;
import java.security.SecureRandom;
import java.security.cert.X509Certificate;
import java.util.Arrays;
import java.util.concurrent.BlockingQueue;
import java.util.concurrent.LinkedBlockingQueue;
import java.util.concurrent.TimeUnit;

/** Black-box scenario driver for the Java AIO client (tcp / tls / ws / wss). */
public final class BBClientAio {

    private static final BlockingQueue<byte[]> INBOX = new LinkedBlockingQueue<>();

    private static String arg(String[] a, String name, String def) {
        for (int i = 0; i < a.length - 1; i++) {
            if (a[i].equals(name)) {
                return a[i + 1];
            }
        }
        return def;
    }

    private static boolean flag(String[] a, String name) {
        return Arrays.asList(a).contains(name);
    }

    private static void fail(String msg) {
        System.out.println("RESULT FAIL " + msg);
        System.out.flush();
        System.exit(1);
    }

    public static void main(String[] args) throws Exception {
        String transport = arg(args, "-transport", "tcp");
        String host = arg(args, "-host", "127.0.0.1");
        int port = Integer.parseInt(arg(args, "-port", "19990"));
        String credential = arg(args, "-credential", "java-aio-dev");
        int keepalive = Integer.parseInt(arg(args, "-keepalive", "30"));
        String scenario = arg(args, "-scenario", "basic");
        int count = Integer.parseInt(arg(args, "-count", "5"));
        int size = Integer.parseInt(arg(args, "-size", "262144"));
        int wait = Integer.parseInt(arg(args, "-wait", "15"));
        boolean reconnect = flag(args, "-reconnect");

        RmttClientBuilder b = new RmttClientBuilder()
                .host(host)
                .port(port)
                .credential(credential)
                .keepAliveSeconds(keepalive)
                .connectTimeoutMillis(5000)
                .autoReconnect(reconnect)
                .connectRetry(reconnect)
                .reconnectBaseMillis(200)
                .reconnectJitter(0f)
                .pushHandler(payload -> INBOX.offer(payload));

        if (transport.equals("tls")) {
            b.tls(true).sslContext(trustAll());
        } else if (transport.equals("ws")) {
            b.webSocket(true);
        } else if (transport.equals("wss")) {
            b.tls(true).webSocket(true).sslContext(trustAll());
        } else if (!transport.equals("tcp")) {
            fail("unsupported transport " + transport);
        }

        RmttClient c = b.build();
        ConnectReturnCode code;
        try {
            code = c.connect();
        } catch (Exception e) {
            if (scenario.equals("authreject")) {
                System.out.println("EVENT CONNECT_REJECTED " + e);
                System.out.println("RESULT PASS");
                return;
            }
            fail("connect exception: " + e);
            return;
        }
        if (code != ConnectReturnCode.CONNECT_ACCEPTED) {
            if (scenario.equals("authreject")) {
                System.out.println("EVENT CONNECT_REJECTED code=" + code);
                System.out.println("RESULT PASS");
                c.shutdown();
                return;
            }
            fail("connect rejected code=" + code);
        }
        System.out.println("EVENT CONNECTED code=" + code);
        System.out.flush();

        try {
            switch (scenario) {
                case "basic": {
                    byte[] msg = "hello-from-java-aio".getBytes(StandardCharsets.UTF_8);
                    push(c, msg);
                    waitEcho(echoFor(msg));
                    break;
                }
                case "concurrent": {
                    for (int i = 0; i < count; i++) {
                        push(c, ("msg-" + i).getBytes(StandardCharsets.UTF_8));
                    }
                    for (int i = 0; i < count; i++) {
                        waitEcho(("echo:msg-" + i).getBytes(StandardCharsets.UTF_8));
                    }
                    System.out.println("EVENT ECHO_OK count=" + count);
                    break;
                }
                case "large":
                case "binary": {
                    byte[] payload;
                    if (scenario.equals("large")) {
                        StringBuilder sb = new StringBuilder(size + 16);
                        while (sb.length() < size) {
                            sb.append("0123456789abcdef");
                        }
                        sb.setLength(size);
                        payload = sb.toString().getBytes(StandardCharsets.US_ASCII);
                    } else {
                        payload = new byte[256];
                        for (int i = 0; i < 256; i++) {
                            payload[i] = (byte) i;
                        }
                    }
                    push(c, payload);
                    waitEcho(echoFor(payload));
                    break;
                }
                case "downlink": {
                    byte[] p = INBOX.poll(wait, TimeUnit.SECONDS);
                    if (p == null) {
                        fail("no downstream push");
                    }
                    System.out.println("EVENT RECV len=" + p.length);
                    break;
                }
                case "hold": {
                    System.out.println("EVENT READY");
                    System.out.flush();
                    Thread.sleep(wait * 1000L);
                    if (!c.isConnected()) {
                        fail("connection lost while holding");
                    }
                    System.out.println("EVENT STILL_CONNECTED");
                    break;
                }
                case "kick": {
                    System.out.println("EVENT READY");
                    System.out.flush();
                    long deadline = System.currentTimeMillis() + wait * 1000L;
                    boolean lost = false;
                    while (System.currentTimeMillis() < deadline) {
                        if (!c.isConnected()) {
                            lost = true;
                            break;
                        }
                        Thread.sleep(100);
                    }
                    if (!lost) {
                        fail("no disconnect observed");
                    }
                    System.out.println("EVENT DISCONNECTED");
                    break;
                }
                case "takeover-attacker": {
                    System.out.println("EVENT READY");
                    System.out.flush();
                    Thread.sleep(wait * 1000L);
                    if (!c.isConnected()) {
                        fail("attacker lost its session");
                    }
                    push(c, "attacker-alive".getBytes(StandardCharsets.UTF_8));
                    waitEcho(echoFor("attacker-alive".getBytes(StandardCharsets.UTF_8)));
                    break;
                }
                case "keepalive": {
                    Thread.sleep(4000);
                    if (!c.isConnected()) {
                        fail("connection reaped despite heartbeat");
                    }
                    push(c, "after-pings".getBytes(StandardCharsets.UTF_8));
                    waitEcho(echoFor("after-pings".getBytes(StandardCharsets.UTF_8)));
                    System.out.println("EVENT KEEPALIVE_OK");
                    break;
                }
                case "reconnect": {
                    push(c, "before".getBytes(StandardCharsets.UTF_8));
                    waitEcho(echoFor("before".getBytes(StandardCharsets.UTF_8)));
                    System.out.println("EVENT READY_FOR_KILL");
                    System.out.flush();
                    Thread.sleep(3000);
                    long deadline = System.currentTimeMillis() + 25000;
                    boolean ok = false;
                    while (System.currentTimeMillis() < deadline) {
                        if (c.isConnected() && c.push("after".getBytes(StandardCharsets.UTF_8))) {
                            byte[] p = INBOX.poll(700, TimeUnit.MILLISECONDS);
                            if (p != null && Arrays.equals(p, echoFor("after".getBytes(StandardCharsets.UTF_8)))) {
                                ok = true;
                                break;
                            }
                        }
                        Thread.sleep(200);
                    }
                    if (!ok) {
                        fail("did not reconnect / echo after reconnect");
                    }
                    System.out.println("EVENT RECONNECTED");
                    break;
                }
                default:
                    fail("unknown scenario " + scenario);
            }
        } finally {
            if (!scenario.equals("kick")) {
                c.disconnect();
            }
            c.shutdown();
        }
        System.out.println("RESULT PASS");
        System.out.flush();
        System.exit(0);
    }

    private static SSLContext trustAll() throws Exception {
        SSLContext ctx = SSLContext.getInstance("TLS");
        ctx.init(null, new TrustManager[]{new X509TrustManager() {
            @Override
            public void checkClientTrusted(X509Certificate[] chain, String authType) {
            }

            @Override
            public void checkServerTrusted(X509Certificate[] chain, String authType) {
            }

            @Override
            public X509Certificate[] getAcceptedIssuers() {
                return new X509Certificate[0];
            }
        }}, new SecureRandom());
        return ctx;
    }

    private static void push(RmttClient c, byte[] payload) {
        if (!c.push(payload)) {
            fail("push failed");
        }
        System.out.println("EVENT SENT len=" + payload.length);
        System.out.flush();
    }

    private static void waitEcho(byte[] want) throws InterruptedException {
        long deadline = System.currentTimeMillis() + 15000;
        while (System.currentTimeMillis() < deadline) {
            byte[] got = INBOX.poll(Math.max(1, deadline - System.currentTimeMillis()), TimeUnit.MILLISECONDS);
            if (got == null) {
                break;
            }
            if (Arrays.equals(got, want)) {
                System.out.println("EVENT ECHO_OK len=" + got.length);
                System.out.flush();
                return;
            }
            System.out.println("EVENT ECHO_OTHER len=" + got.length);
        }
        fail("timeout waiting for echo");
    }

    private static byte[] echoFor(byte[] p) {
        byte[] out = new byte[p.length + 5];
        System.arraycopy("echo:".getBytes(StandardCharsets.US_ASCII), 0, out, 0, 5);
        System.arraycopy(p, 0, out, 5, p.length);
        return out;
    }
}
