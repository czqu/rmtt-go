package net.czqu.bb.client;

import io.netty.channel.ChannelFuture;
import io.netty.handler.ssl.SslContext;
import io.netty.handler.ssl.SslContextBuilder;
import io.netty.handler.ssl.util.InsecureTrustManagerFactory;
import net.czqu.rmtt.client.netty.RmttClient;
import net.czqu.rmtt.client.netty.RmttClientBuilder;
import net.czqu.rmtt.protocol.ConnectReturnCode;

import java.nio.charset.StandardCharsets;
import java.util.Arrays;
import java.util.concurrent.BlockingQueue;
import java.util.concurrent.LinkedBlockingQueue;
import java.util.concurrent.TimeUnit;

/** Black-box scenario driver for the Java Netty client (tcp / ws / wss / kcp). */
public final class BBClientNetty {

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
        String credential = arg(args, "-credential", "java-dev");
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

        if (transport.equals("ws")) {
            b.webSocket(true);
        } else if (transport.equals("wss")) {
            b.webSocket(true).secure(true).sslContext(trustAll());
        } else if (transport.equals("kcp")) {
            b.kcp(true);
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
                    byte[] msg = "hello-from-java".getBytes(StandardCharsets.UTF_8);
                    push(c, msg);
                    waitEcho(echoFor(msg));
                    break;
                }
                case "concurrent": {
                    for (int i = 0; i < count; i++) {
                        push(c, ("msg-" + i).getBytes(StandardCharsets.UTF_8));
                    }
                    java.util.Set<String> got = new java.util.HashSet<>();
                    long deadline = System.currentTimeMillis() + 10000;
                    while (got.size() < count && System.currentTimeMillis() < deadline) {
                        byte[] p = INBOX.poll(500, TimeUnit.MILLISECONDS);
                        if (p != null) {
                            got.add(new String(p, StandardCharsets.UTF_8));
                        }
                    }
                    for (int i = 0; i < count; i++) {
                        if (!got.contains("echo:msg-" + i)) {
                            fail("missing echo msg-" + i);
                        }
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
                    Thread.sleep(6000);
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
                        if (c.isConnected()) {
                            try {
                                if (pushQuiet(c, "after".getBytes(StandardCharsets.UTF_8))) {
                                    byte[] p = INBOX.poll(700, TimeUnit.MILLISECONDS);
                                    if (p != null && Arrays.equals(p, echoFor("after".getBytes(StandardCharsets.UTF_8)))) {
                                        ok = true;
                                        break;
                                    }
                                }
                            } catch (RuntimeException ignored) {
                                // still reconnecting
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
            if (scenario.equals("hold") || scenario.equals("takeover-attacker")) {
                c.disconnect();
            } else if (!scenario.equals("kick")) {
                c.disconnect();
            }
            c.shutdown();
        }
        System.out.println("RESULT PASS");
        System.out.flush();
        System.exit(0);
    }

    private static SslContext trustAll() throws Exception {
        return SslContextBuilder.forClient().trustManager(InsecureTrustManagerFactory.INSTANCE).build();
    }

    private static void push(RmttClient c, byte[] payload) {
        boolean ok = pushQuiet(c, payload);
        if (!ok) {
            fail("push failed");
        }
        System.out.println("EVENT SENT len=" + payload.length);
        System.out.flush();
    }

    private static boolean pushQuiet(RmttClient c, byte[] payload) {
        if (!c.isConnected()) {
            return false;
        }
        ChannelFuture f = c.push(payload);
        f.awaitUninterruptibly(10000);
        return f.isSuccess();
    }

    private static void waitEcho(byte[] want) throws InterruptedException {
        long deadline = System.currentTimeMillis() + 6000;
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
