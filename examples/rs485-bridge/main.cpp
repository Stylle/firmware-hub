/** @file main.cpp
 * @brief Qt SerialPort reference bridge; no assumed vendor bootloader protocol.
 * --read is ordinary Modbus FC03 and never writes a register.
 * --probe/--upload use FH485/1 ONLY with a newly integrated receiver.
 * Flashing requires --confirm-write; activation additionally needs --activate.
 */
#include "fw485.h"
#include <QCommandLineParser>
#include <QCoreApplication>
#include <QCryptographicHash>
#include <QElapsedTimer>
#include <QFile>
#include <QRandomGenerator>
#include <QSerialPort>
#include <QSerialPortInfo>
#include <QTextStream>
#include <QThread>
#include <cstring>

static quint16 word(const QByteArray &b, int pos) {
    return quint16(quint8(b[pos])) * 256 + quint8(b[pos + 1]);
}
static void put32(QByteArray &b, int pos, quint32 n) {
    for (int i = 0; i < 4; i++)
        b[pos + i] = char(n >> (24 - i * 8));
}
static QByteArray transact(QSerialPort &port, const QByteArray &frame, bool custom,
                           QString &error) {
    for (int attempt = 0; attempt < 3; attempt++) {
        port.clear(QSerialPort::Input);
        if (port.write(frame) != frame.size() ||
            (!port.waitForBytesWritten(1500) && port.bytesToWrite() > 0)) {
            error = port.errorString();
            return {};
        }
        QByteArray reply;
        QElapsedTimer clock;
        clock.start();
        int expected = 0;
        const int deadline = custom ? 15000 : 2500;
        while (clock.elapsed() < deadline) {
            if (port.bytesAvailable() == 0)
                port.waitForReadyRead(qMax(1, deadline - int(clock.elapsed())));
            reply += port.readAll();
            if (reply.size() > int(FW485_FRAME_MAX)) {
                error = "reply exceeds frame bound";
                return {};
            }
            if (custom && reply.size() >= 4) {
                if (quint8(reply[1]) != 0x41 || word(reply, 2) > FW485_PAYLOAD_MAX) {
                    error = "not an FH485/1 receiver";
                    return {};
                };
                expected = 15 + word(reply, 2);
            }
            if (!custom && reply.size() >= 3)
                expected = (quint8(reply[1]) & 0x80) ? 5
                           : quint8(frame[1]) == 6   ? 8
                                                     : 5 + quint8(reply[2]);
            if (expected && reply.size() >= expected) {
                if (reply.size() != expected || quint8(reply[0]) != quint8(frame[0]) ||
                    fw485_crc16(reinterpret_cast<const uint8_t *>(reply.constData()),
                                size_t(reply.size())) != 0) {
                    error = "invalid reply address/length/CRC";
                    break;
                }
                return reply;
            }
        }
    }
    error = "no valid reply after 3 bounded attempts; no alternate baud/address scan performed";
    return {};
}
int main(int argc, char **argv) {
    QCoreApplication app(argc, argv);
    QCommandLineParser parser;
    parser.setApplicationDescription(
        "Firmware Hub Qt RS485 bridge: read-only Modbus or explicitly integrated FH485/1");
    parser.addHelpOption();
    parser.addOptions(
        {{QStringList{"p", "port"}, "Serial port (explicit, e.g. COM3)", "port"},
         {"baud", "Baud rate", "baud", "9600"},
         {"parity", "none/even/odd", "parity", "none"},
         {"address", "Unicast address 1..247", "address", "1"},
         {"read", "Read Modbus holding registers (FC03 only)"},
         {"register", "Raw holding-register address 0..65535", "register", "0"},
         {"count", "Register count 1..32", "count", "1"},
         {"ports", "List available ports without opening them"},
         {"probe", "FH485/1 HELLO (only for integrated new bootloader)"},
         {"upload", "Transfer staged image to an FH485/1 receiver", "file"},
         {"sha256", "Expected SHA-256 from verified download metadata", "hex"},
         {"version", "Target version identifier", "version"},
         {"hardware", "Exact target hardware identifier", "hardware"},
         {"confirm-write", "Explicitly permit staging writes; active image must remain intact"},
         {"activate", "After verification, explicitly request safe boot installation"}});
    parser.addOption(
        {"enter-boot",
         "NEW N32 demo only: write register 33=0xB007 before FH485; requires --confirm-write"});
    parser.process(app);
    QTextStream output(stdout), error(stderr);
    if (parser.isSet("ports")) {
        for (const auto &info : QSerialPortInfo::availablePorts())
            output << info.portName() << "\n";
        return 0;
    }
    auto fail = [&](const QString &why) {
        error << why << "\n";
        error.flush();
        return 1;
    };
    if (int(parser.isSet("read")) + int(parser.isSet("probe")) + int(parser.isSet("upload")) != 1 ||
        !parser.isSet("port"))
        return fail("choose exactly one of --read, --probe or --upload and provide --port");
    if ((parser.isSet("upload") || parser.isSet("enter-boot")) && !parser.isSet("confirm-write"))
        return fail("upload requires --confirm-write and a board-specific, safe staging "
                    "bootloader; nothing sent");
    if (parser.isSet("upload")) {
        QFile input(parser.value("upload"));
        QCryptographicHash hash(QCryptographicHash::Sha256);
        if (!input.open(QIODevice::ReadOnly) || input.size() < 1 || input.size() > (1LL << 30) ||
            !hash.addData(&input) ||
            QString::fromLatin1(hash.result().toHex()) != parser.value("sha256").toLower())
            return fail("local image/hash invalid; no port command sent");
        if (parser.value("version").isEmpty() || parser.value("version").size() > 64 ||
            parser.value("hardware").isEmpty() || parser.value("hardware").size() > 64)
            return fail("version/hardware missing; no port command sent");
    }
    bool ok = false;
    int address = parser.value("address").toInt(&ok);
    if (!ok || address < 1 || address > 247)
        return fail("invalid unicast address");
    int baud = parser.value("baud").toInt(&ok);
    if (!ok || baud < 1200 || baud > 1000000)
        return fail("invalid baud rate");
    QSerialPort port;
    port.setPortName(parser.value("port"));
    port.setBaudRate(baud);
    port.setDataBits(QSerialPort::Data8);
    port.setStopBits(QSerialPort::OneStop);
    port.setFlowControl(QSerialPort::NoFlowControl);
    const auto parity = parser.value("parity");
    if (parity == "none")
        port.setParity(QSerialPort::NoParity);
    else if (parity == "even")
        port.setParity(QSerialPort::EvenParity);
    else if (parity == "odd")
        port.setParity(QSerialPort::OddParity);
    else
        return fail("invalid parity");
    if (!port.open(QIODevice::ReadWrite))
        return fail("cannot open port: " + port.errorString());
    QString why;
    if (parser.isSet("enter-boot")) {
        if (!parser.isSet("upload"))
            return fail("--enter-boot is only allowed with an explicit upload");
        QByteArray frame = QByteArray::fromHex("01060021b0070000");
        frame[0] = char(address);
        auto crc = fw485_crc16(reinterpret_cast<const uint8_t *>(frame.constData()), 6);
        frame[6] = char(crc);
        frame[7] = char(crc >> 8);
        const auto reply = transact(port, frame, false, why);
        if (reply != frame)
            return fail("boot-entry command was not acknowledged; no FH485 transfer started");
        QThread::msleep(300);
    }
    if (parser.isSet("read")) {
        int start = parser.value("register").toInt(&ok);
        if (!ok || start < 0 || start > 65535)
            return fail("invalid register");
        int count = parser.value("count").toInt(&ok);
        if (!ok || count < 1 || count > 32 || start + count > 65536)
            return fail("invalid register count");
        QByteArray f(8, 0);
        f[0] = char(address);
        f[1] = 3;
        f[2] = char(start >> 8);
        f[3] = char(start);
        f[4] = char(count >> 8);
        f[5] = char(count);
        auto crc = fw485_crc16(reinterpret_cast<const uint8_t *>(f.constData()), 6);
        f[6] = char(crc);
        f[7] = char(crc >> 8);
        const auto response = transact(port, f, false, why);
        if (response.isEmpty())
            return fail(why);
        if (quint8(response[1]) != 3 || quint8(response[2]) != count * 2)
            return fail("Modbus exception or unexpected byte count");
        for (int i = 0; i < count; i++)
            output << "register " << start + i << " = " << word(response, 3 + i * 2) << "\n";
        return 0;
    }
    quint32 session = QRandomGenerator::global()->generate();
    if (!session)
        session = 1;
    quint32 sequence = 1;
    auto exchange = [&](uint8_t command, const QByteArray &payload) -> QByteArray {
        uint8_t frame[FW485_FRAME_MAX];
        fw485_packet p{uint8_t(address),
                       command,
                       session,
                       sequence,
                       reinterpret_cast<const uint8_t *>(payload.constData()),
                       size_t(payload.size())};
        auto n = fw485_encode(&p, frame, sizeof(frame));
        if (!n) {
            why = "invalid outbound frame";
            return {};
        };
        auto response =
            transact(port, QByteArray(reinterpret_cast<char *>(frame), int(n)), true, why);
        fw485_packet ack{};
        if (!fw485_decode(reinterpret_cast<const uint8_t *>(response.constData()),
                          size_t(response.size()), &ack) ||
            ack.command != (command | 0x80) || ack.session != session || ack.sequence != sequence ||
            ack.length < 10) {
            why = "invalid FH485 ACK";
            return {};
        }
        if (ack.payload[0] != FW485_OK) {
            why = "device refused request, status=" + QString::number(ack.payload[0]);
            return {};
        };
        sequence++;
        return QByteArray(reinterpret_cast<const char *>(ack.payload), int(ack.length));
    };
    auto hello = exchange(FW485_HELLO, {});
    if (hello.size() != 140)
        return fail(why.isEmpty() ? "receiver HELLO incompatible" : why);
    output << "FH485 receiver: version="
           << QString::fromLatin1(hello.constData() + 10, 65).section(QChar(0), 0, 0)
           << " hardware="
           << QString::fromLatin1(hello.constData() + 75, 65).section(QChar(0), 0, 0) << "\n";
    output.flush();
    if (parser.isSet("probe"))
        return 0;
    QFile file(parser.value("upload"));
    if (!file.open(QIODevice::ReadOnly) || file.size() < 1 || file.size() > 1LL << 30)
        return fail("image unreadable or exceeds 1 GiB");
    QCryptographicHash hash(QCryptographicHash::Sha256);
    if (!hash.addData(&file))
        return fail("cannot hash image");
    const auto expected = hash.result();
    if (QString::fromLatin1(expected.toHex()) != parser.value("sha256").toLower())
        return fail("image SHA-256 mismatch; BEGIN not sent");
    file.seek(0);
    const auto version = parser.value("version").toLatin1(),
               hardware = parser.value("hardware").toLatin1();
    if (version.isEmpty() || version.size() > 64 || hardware.isEmpty() || hardware.size() > 64)
        return fail("version/hardware missing or too long");
    QByteArray begin(166, 0);
    put32(begin, 0, quint32(file.size()));
    std::memcpy(begin.data() + 4, expected.constData(), 32);
    std::memcpy(begin.data() + 36, version.constData(), size_t(version.size()));
    std::memcpy(begin.data() + 101, hardware.constData(), size_t(hardware.size()));
    if (exchange(FW485_BEGIN, begin).isEmpty())
        return fail(why);
    quint32 offset = 0;
    while (!file.atEnd()) {
        auto block = file.read(FW485_PAYLOAD_MAX - 4);
        if (block.isEmpty())
            return fail("local file read failed; staging not activated");
        QByteArray data(4, 0);
        put32(data, 0, offset);
        data += block;
        if (exchange(FW485_DATA, data).isEmpty())
            return fail(why);
        offset += quint32(block.size());
        if (offset % 3008u == 0u || file.atEnd()) {
            output << "Transferred " << offset << " / " << file.size() << " bytes\n";
            output.flush();
        }
    }
    if (exchange(FW485_VERIFY, {}).isEmpty())
        return fail(why);
    output << "Staged image SHA-256 independently verified by receiver; not yet installed.\n";
    if (parser.isSet("activate")) {
        if (exchange(FW485_COMMIT, {}).isEmpty())
            return fail(why);
        output << "Safe activation requested; reconnect and read installed version to confirm "
                  "success.\n";
    }
    return 0;
}
