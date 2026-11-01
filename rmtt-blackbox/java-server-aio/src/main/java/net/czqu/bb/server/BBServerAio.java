package net.czqu.bb.server;

import net.czqu.rmtt.api.AuthResult;
import net.czqu.rmtt.api.PushResult;
import net.czqu.rmtt.protocol.ConnectReturnCode;
import net.czqu.rmtt.protocol.DisconnectReturnCode;
import net.czqu.rmtt.protocol.ServerKeepalivePolicy;
import net.czqu.rmtt.server.aio.RmttServer;
import net.czqu.rmtt.server.aio.RmttServerBuilder;

import javax.net.ssl.KeyManagerFactory;
import javax.net.ssl.SSLContext;
import java.io.BufferedReader;
import java.io.ByteArrayOutputStream;
import java.io.FileInputStream;
import java.io.InputStream;
import java.io.InputStreamReader;
import java.io.OutputStreamWriter;
import java.io.PrintWriter;
import java.net.ServerSocket;
import java.net.Socket;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Paths;
import java.security.KeyFactory;
import java.security.KeyStore;
import java.security.cert.Certificate;
import java.security.cert.CertificateFactory;
import java.security.cert.X509Certificate;
import java.security.spec.PKCS8EncodedKeySpec;
import java.util.Arrays;
import java.util.Base64;
import java.util.HashSet;
import java.util.Set;

/** Black-box test server for the Java AIO stack (tcp / tls / ws / wss). */
public final class BBServerAio {

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

    public static void main(String[] args) throws Exception {
        String transport = arg(args, "-transport", "tcp");
        int port = Integer.parseInt(arg(args, "-port", "19990"));
        int control = Integer.parseInt(arg(args, "-control", "0"));
        int min = Integer.parseInt(arg(args, "-keepalive-min", "30"));
        int max = Integer.parseInt(arg(args, "-keepalive-max", "600"));
        int def = Integer.parseInt(arg(args, "-keepalive-default", "60"));
        boolean allowDisable = flag(args, "-allow-disable");
        boolean noEcho = flag(args, "-no-echo");
        String cert = arg(args, "-cert", "");
        String key = arg(args, "-key", "");
        Set<String> reject = new HashSet<>();
        for (String s : arg(args, "-reject", "").split(",")) {
            if (!s.isEmpty()) {
                reject.add(s);
            }
        }

        final RmttServer[] ref = new RmttServer[1];
        RmttServerBuilder b = new RmttServerBuilder()
                .authenticator(credential -> reject.contains(credential)
                        ? AuthResult.reject(ConnectReturnCode.CONNECT_UNAUTHORIZED)
                        : AuthResult.allow(credential))
                .keepalivePolicy(new ServerKeepalivePolicy(min, max, def, allowDisable))
                .messageHandler((deviceId, payload) -> {
                    String text = new String(payload, StandardCharsets.UTF_8);
                    System.out.println("PUSH device=" + deviceId + " len=" + payload.length
                            + " payload=\"" + text.substring(0, Math.min(120, text.length())) + "\"");
                    System.out.flush();
                    if (!noEcho) {
                        byte[] echo = new byte[5 + payload.length];
                        System.arraycopy("echo:".getBytes(StandardCharsets.US_ASCII), 0, echo, 0, 5);
                        System.arraycopy(payload, 0, echo, 5, payload.length);
                        ref[0].push(deviceId, echo);
                    }
                });

        boolean needTls = transport.equals("tls") || transport.equals("wss");
        if (needTls) {
            b.sslContext(buildSslContext(cert, key));
        }
        if (transport.equals("tcp")) {
            b.port(port);
        } else if (transport.equals("tls")) {
            b.port(0).tlsPort(port);
        } else if (transport.equals("ws")) {
            b.port(0).wsPort(port);
        } else if (transport.equals("wss")) {
            b.port(0).wssPort(port);
        } else {
            System.err.println("unsupported transport for aio server: " + transport);
            System.exit(2);
        }

        RmttServer server = b.build();
        ref[0] = server;
        server.start();
        if (control > 0) {
            startControl(control, server);
        }
        System.out.println("READY transport=" + transport + " port=" + port + " control=" + control);
        System.out.flush();
        Thread.currentThread().join();
    }

    private static SSLContext buildSslContext(String certPath, String keyPath) throws Exception {
        CertificateFactory cf = CertificateFactory.getInstance("X.509");
        X509Certificate cert;
        try (InputStream in = new FileInputStream(certPath)) {
            cert = (X509Certificate) cf.generateCertificate(in);
        }
        String pem = new String(Files.readAllBytes(Paths.get(keyPath)), StandardCharsets.US_ASCII);
        String base64 = pem.replace("-----BEGIN PRIVATE KEY-----", "")
                .replace("-----END PRIVATE KEY-----", "")
                .replaceAll("\\s", "");
        byte[] der = Base64.getDecoder().decode(base64);
        java.security.PrivateKey priv = KeyFactory.getInstance("RSA")
                .generatePrivate(new PKCS8EncodedKeySpec(der));

        KeyStore ks = KeyStore.getInstance("PKCS12");
        ks.load(null, null);
        char[] pw = new char[0];
        ks.setKeyEntry("server", priv, pw, new Certificate[]{cert});
        KeyManagerFactory kmf = KeyManagerFactory.getInstance(KeyManagerFactory.getDefaultAlgorithm());
        kmf.init(ks, pw);
        SSLContext ctx = SSLContext.getInstance("TLS");
        ctx.init(kmf.getKeyManagers(), null, null);
        return ctx;
    }

    private static void startControl(int port, final RmttServer server) throws Exception {
        final ServerSocket ss = new ServerSocket(port, 50,
                java.net.InetAddress.getByName("127.0.0.1"));
        System.out.println("CONTROL_READY port=" + port);
        System.out.flush();
        Thread t = new Thread(() -> {
            while (true) {
                try {
                    final Socket s = ss.accept();
                    new Thread(() -> handle(s, server)).start();
                } catch (Exception e) {
                    return;
                }
            }
        });
        t.setDaemon(true);
        t.start();
    }

    private static void handle(Socket socket, RmttServer server) {
        try (BufferedReader in = new BufferedReader(
                new InputStreamReader(socket.getInputStream(), StandardCharsets.UTF_8));
             PrintWriter out = new PrintWriter(new OutputStreamWriter(
                     socket.getOutputStream(), StandardCharsets.UTF_8), true)) {
            String line;
            while ((line = in.readLine()) != null) {
                line = line.trim();
                if (line.isEmpty()) {
                    continue;
                }
                System.out.println("CONTROL " + line);
                System.out.flush();
                String[] p = line.split(" ", 3);
                if (p[0].equals("PING")) {
                    out.println("OK PONG");
                } else if (p[0].equals("PUSH") && p.length == 3) {
                    PushResult r = server.push(p[1], p[2].getBytes(StandardCharsets.UTF_8));
                    out.println(r == PushResult.SUCCESS ? "OK" : "ERR " + r);
                } else if (p[0].equals("KICK") && p.length == 3) {
                    int code = Integer.parseInt(p[2]);
                    server.kick(p[1], DisconnectReturnCode.valueOf((byte) code));
                    out.println("OK");
                } else if (p[0].equals("ONLINE") && p.length >= 2) {
                    out.println("OK " + server.isOnline(p[1]));
                } else if (p[0].equals("COUNT")) {
                    out.println("OK " + server.onlineCount());
                } else if (p[0].equals("SHUTDOWN")) {
                    out.println("OK");
                    out.flush();
                    socket.close();
                    server.closeAll();
                    System.exit(0);
                } else {
                    out.println("ERR unknown");
                }
            }
        } catch (Exception e) {
            // client went away
        }
    }
}
