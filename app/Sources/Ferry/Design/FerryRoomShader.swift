// The room and its glass as one Metal fragment shader, compiled when the app starts. Ported
// from the Lunar Park style kit (park/apple/Sources/ParkKit/RoomShader.swift): the same
// refraction, lens rim, colour fringing, five-tap frost and pane shadows, with the room
// retuned from Park's graphite and amber to Ferry's water.
//
// The room is computed, not stored: deep graphite-navy washed with steel blue at the upper
// left and a sea-slate swell at the lower right, a cool light wandering between them, and
// three soft orbs of wake mist and slate, with no grain: it made text harder to read. Glass
// is the same room evaluated at bent positions. Inside a pane, drifting fractal noise shoves
// each sample sideways, more at the rim than in the middle. The rim acts like the thick edge
// of a lens: it pulls what is behind it inward with a little colour fringing, and five taps
// frost the result. Outside the panes each one drops a soft shadow.
//
// Ferry adds one thing Park's pages never needed: each pane carries the rectangle it is
// visible in, so a card scrolled under the head stops bending the room there.

enum FerryRoomShader {
  static let source = #"""
    #include <metal_stdlib>
    using namespace metal;

    struct Out { float4 position [[position]]; };

    struct Pane { float4 rect; float4 extra; float4 clip; };   // x, y, w, h; radius; visible x, y, w, h

    /// The six lights' centres for this frame, in window points. They move with time only,
    /// so the CPU works them out once a frame.
    struct Lights { float2 centre[6]; };

    vertex Out roomVertex(uint id [[vertex_id]]) {
        float2 p = float2((id << 1) & 2, id & 2);
        Out out;
        out.position = float4(p * 2.0 - 1.0, 0.0, 1.0);
        return out;
    }

    static float hash21(float2 p) {
        p = fract(p * float2(123.34, 456.21));
        p += dot(p, p + 45.32);
        return fract(p.x * p.y);
    }

    static float valueNoise(float2 p) {
        float2 i = floor(p);
        float2 f = fract(p);
        float2 u = f * f * (3.0 - 2.0 * f);
        return mix(mix(hash21(i), hash21(i + float2(1, 0)), u.x),
                   mix(hash21(i + float2(0, 1)), hash21(i + float2(1, 1)), u.x), u.y);
    }

    static float fbm(float2 p) {
        float value = 0.0, amplitude = 0.5;
        for (int octave = 0; octave < 4; octave++) {
            value += amplitude * valueNoise(p);
            p = p * 2.03 + float2(1.7, 9.2);
            amplitude *= 0.5;
        }
        return value;
    }

    static float roundedBox(float2 p, float2 half_, float radius) {
        float2 q = abs(p) - half_ + radius;
        return length(max(q, 0.0)) + min(max(q.x, q.y), 0.0) - radius;
    }

    static float light(float2 position, float2 centre, float radius, float hard) {
        float f = clamp(length(position - centre) / max(radius, 1.0), 0.0, 1.0);
        if (hard <= 0.0) return 1.0 - f;
        return f < hard ? mix(1.0, 0.85, f / hard) : 0.85 * (1.0 - (f - hard) / (1.0 - hard));
    }

    static float3 rgb(float r, float g, float b) { return float3(r, g, b) / 255.0; }

    /// The room at one point, in window points.
    static float3 room(float2 position, float2 size, constant Lights &lights) {
        float w = max(size.x, 1.0), h = max(size.y, 1.0);
        float span = max(w, h);
        float2 a = float2(w * 0.1, 0.0), b = float2(w * 0.9, h);
        float along = clamp(dot(position - a, b - a) / dot(b - a, b - a), 0.0, 1.0);
        // #162235 at the upper left, through #131C2B, to #101722 at the lower right.
        float3 colour = along < 0.5 ? mix(rgb(0x16, 0x22, 0x35), rgb(0x13, 0x1C, 0x2B), along * 2.0)
                                    : mix(rgb(0x13, 0x1C, 0x2B), rgb(0x10, 0x17, 0x22), (along - 0.5) * 2.0);
        colour = mix(colour, rgb(117, 161, 198), 0.19 * light(position, lights.centre[0], span * 0.80, 0.0));  // steel blue
        colour = mix(colour, rgb(70, 124, 152), 0.21 * light(position, lights.centre[1], span * 0.80, 0.0));   // sea-slate swell
        colour = mix(colour, rgb(104, 132, 196), 0.09 * light(position, lights.centre[2], span * 0.42, 0.0));  // wandering cool light
        colour = mix(colour, rgb(158, 197, 230), 0.085 * light(position, lights.centre[3], span * 0.13, 0.45)); // wake mist
        colour = mix(colour, rgb(92, 132, 166), 0.10 * light(position, lights.centre[4], span * 0.15, 0.45));  // deep sea slate
        colour = mix(colour, rgb(146, 138, 214), 0.08 * light(position, lights.centre[5], span * 0.10, 0.40)); // dusk
        return colour;
    }

    static bool visible(float2 position, float4 clip) {
        return position.x >= clip.x && position.y >= clip.y
            && position.x <= clip.x + clip.z && position.y <= clip.y + clip.w;
    }

    fragment half4 roomFragment(Out in [[stage_in]],
                                constant float4 &frame [[buffer(0)]],      // width, height (points), scale, time
                                constant int4 &counts [[buffer(1)]],       // panes, bend on, 0, 0
                                constant Pane *panes [[buffer(2)]],
                                constant Lights &lights [[buffer(3)]]) {
        float2 size = frame.xy;
        float t = frame.w;
        float2 position = in.position.xy / frame.z;

        float inside = 1e5;
        int which = -1;
        float shade = 0.0;
        for (int i = 0; i < counts.x; i++) {
            if (!visible(position, panes[i].clip)) continue;
            float2 half_ = panes[i].rect.zw * 0.5;
            float2 centre = panes[i].rect.xy + half_;
            // Farther than a pane's shadow reaches (34 pt, 10 pt down), it changes nothing here.
            float2 far = abs(position - centre) - half_ - 44.0;
            if (far.x > 0.0 || far.y > 0.0) continue;
            float d = roundedBox(position - centre, half_, panes[i].extra.x);
            if (d < inside) { inside = d; which = i; }
            float below = roundedBox(position - centre - float2(0.0, 10.0), half_, panes[i].extra.x);
            shade = max(shade, (1.0 - smoothstep(-12.0, 34.0, below)) * 0.30);
        }

        if (which < 0 || inside > 0.0) {
            return half4(half3(room(position, size, lights) * (1.0 - shade)), 1.0h);
        }

        float2 half_ = panes[which].rect.zw * 0.5;
        float2 p = position - panes[which].rect.xy - half_;
        float radius = panes[which].extra.x;
        float2 normal = float2(roundedBox(p + float2(1, 0), half_, radius) - roundedBox(p - float2(1, 0), half_, radius),
                               roundedBox(p + float2(0, 1), half_, radius) - roundedBox(p - float2(0, 1), half_, radius));
        normal /= max(length(normal), 1e-4);

        const float bend = 14.0, rim = 24.0, lens = 14.0, frost = 5.0;
        float edge = 1.0 - smoothstep(0.0, rim, -inside);
        float2 q = position * 0.006;
        float2 wobble = float2(fbm(q + float2(t * 0.060, t * 0.013)),
                               fbm(q + float2(5.2, 1.3) - float2(t * 0.017, t * 0.050))) - 0.5;
        float2 bent = position + wobble * bend * (0.45 + 1.55 * edge) - normal * lens * edge * edge;

        float angle = wobble.x * 6.2831;
        float2 da = float2(cos(angle), sin(angle)) * frost;
        float2 db = float2(-da.y, da.x);
        float3 mid = (room(bent, size, lights) * 2.0 + room(bent + da, size, lights) + room(bent - da, size, lights)
                      + room(bent + db, size, lights) + room(bent - db, size, lights)) / 6.0;
        float fringe = 1.6 * edge;
        float red = room(bent + normal * fringe, size, lights).r;
        float blue = room(bent - normal * fringe, size, lights).b;
        float3 colour = float3(mix(mid.r, red, edge * 0.6), mid.g, mix(mid.b, blue, edge * 0.6));
        // Glass gathers light: a touch brighter and more saturated than the room behind it.
        float luma = dot(colour, float3(0.299, 0.587, 0.114));
        colour = mix(float3(luma), colour, 1.35) * 1.04;
        return half4(half3(colour), 1.0h);
    }
    """#
}
