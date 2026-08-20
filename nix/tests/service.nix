{
  pkgs,
  module,
  package,
}:

let
  listenAddress = "127.0.0.1";
  port = 5678;
  baseUrl = "http://${listenAddress}:${toString port}";
  metricsPort = 9104;
  metricsUrl = "http://${listenAddress}:${toString metricsPort}/metrics";
  token = "1234567890:telegym_test_token";
in
pkgs.testers.nixosTest {
  name = "telegym-service";

  containers.machine = {
    imports = [ module ];

    environment.systemPackages = [ pkgs.curl ];

    services.telegym-mock = {
      enable = true;
      inherit package listenAddress port;
      metrics = {
        enable = true;
        inherit listenAddress;
        port = metricsPort;
      };
    };
  };

  testScript = ''
    import json
    import shlex

    start_all()
    machine.wait_for_unit("telegym-mock.service")

    def request(path, method="GET", body=None):
        command = ["curl", "-fsS", "-X", method]
        if body is not None:
            command.extend([
                "-H", "Content-Type: application/json",
                "--data", json.dumps(body, separators=(",", ":")),
            ])
        command.append("${baseUrl}" + path)
        return json.loads(machine.succeed(" ".join(map(shlex.quote, command))))

    with subtest("serve health and Bot API endpoints"):
        health = request("/health")
        assert health["status"] == "ok", health

        me = request("/bot${token}/getMe")
        assert me["ok"] and me["result"]["is_bot"], me

    with subtest("deliver polling updates"):
        injected = request(
            "/debug/inject/update",
            "POST",
            {
                "token": "${token}",
                "chat_id": 42,
                "user_id": 7,
                "first_name": "Test",
                "text": "ping",
            },
        )
        assert injected["ok"] and injected["delivery_method"] == "polling", injected

        updates = request("/bot${token}/getUpdates", "POST", {"timeout": 0})
        assert updates["ok"] and len(updates["result"]) == 1, updates
        assert updates["result"][0]["message"]["text"] == "ping", updates

    with subtest("capture outbound messages"):
        sent = request(
            "/bot${token}/sendMessage",
            "POST",
            {"chat_id": 42, "text": "pong"},
        )
        assert sent["ok"] and sent["result"]["text"] == "pong", sent

        captured = request("/debug/messages/${token}?chat_id=42")
        assert captured["count"] == 1, captured
        assert captured["messages"][0]["text"] == "pong", captured

    with subtest("export request metrics"):
        machine.wait_until_succeeds(
            "curl -fsS ${metricsUrl} | "
            "grep -F 'telegym_mock_requests_total{method=\"sendMessage\",status=\"200\"} 1'"
        )
  '';
}
