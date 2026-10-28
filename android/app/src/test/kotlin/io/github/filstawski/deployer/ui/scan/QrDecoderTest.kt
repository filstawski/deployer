package io.github.filstawski.deployer.ui.scan

import com.google.zxing.BarcodeFormat
import com.google.zxing.qrcode.QRCodeWriter
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

class QrDecoderTest {
    private val link = "deployer://pair?server=https%3A%2F%2Fapi.example.com&token=dpl_" + "ab".repeat(32)

    /** A camera-like frame: the code in the middle of a larger gray image. */
    private fun frame(text: String, inverted: Boolean): Pair<ByteArray, Int> {
        val matrix = QRCodeWriter().encode(text, BarcodeFormat.QR_CODE, 300, 300)
        val size = 480
        val offset = (size - matrix.width) / 2
        val pixels = ByteArray(size * size) { 0x80.toByte() }
        for (y in 0 until matrix.height) for (x in 0 until matrix.width) {
            val dark = matrix[x, y] != inverted
            pixels[(y + offset) * size + x + offset] = if (dark) 0x10 else 0xF0.toByte()
        }
        return pixels to size
    }

    @Test fun readsCodes() {
        val (pixels, size) = frame(link, inverted = false)
        assertEquals(link, QrDecoder().decode(pixels, size, size))
    }

    @Test fun readsLightOnDarkCodes() {
        // How deployer pair's code looks in a terminal without colors.
        val (pixels, size) = frame(link, inverted = true)
        assertEquals(link, QrDecoder().decode(pixels, size, size))
    }

    @Test fun findsNothingInAnEmptyFrame() {
        assertNull(QrDecoder().decode(ByteArray(64 * 64) { 0x40 }, 64, 64))
    }
}
