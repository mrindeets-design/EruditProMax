(function () {
    "use strict";

    // =========================================================
    // НАСТРОЙКИ
    // =========================================================

    const AVATAR_URL = "/widget/assets/erudit.png";
    const WELCOME_URL = "/widget/welcome.html";


    // =========================================================
    // УДАЛЯЕМ ПРЕДЫДУЩИЙ ВИДЖЕТ
    // =========================================================

    const oldHost = document.getElementById("erudit-launcher");

    if (oldHost) {
        oldHost.remove();
    }


    // =========================================================
    // СОЗДАЁМ HOST
    // =========================================================

    const host = document.createElement("div");

    host.id = "erudit-launcher";

    host.style.position = "fixed";
    host.style.inset = "0";
    host.style.zIndex = "999999";
    host.style.pointerEvents = "none";

    document.body.appendChild(host);


    // =========================================================
    // SHADOW DOM
    // =========================================================

    const shadow = host.attachShadow({
        mode: "open"
    });


    // =========================================================
    // HTML + CSS
    // =========================================================

    shadow.innerHTML = `

        <style>

            * {
                box-sizing: border-box;
            }


            /* =================================================
               КНОПКА
               ================================================= */

            .launcher {

                position: fixed;

                right: 24px;
                bottom: 24px;

                width: 72px;
                height: 72px;

                display: flex;

                align-items: center;
                justify-content: center;

                padding: 0;

                border: 4px solid #ffffff;
                border-radius: 50%;

                background:
                    linear-gradient(
                        145deg,
                        #0a57aa,
                        #032f67
                    );

                box-shadow:
                    0 14px 40px
                    rgba(0, 43, 94, 0.35);

                cursor: pointer;

                pointer-events: auto;

                text-decoration: none;

                transition:
                    transform .2s ease,
                    box-shadow .2s ease;
            }


            .launcher:hover {

                transform:
                    translateY(-3px)
                    scale(1.04);

                box-shadow:
                    0 18px 48px
                    rgba(0, 43, 94, 0.42);
            }


            .launcher img {

                width: 60px;
                height: 60px;

                object-fit: contain;

                border-radius: 50%;

                display: block;
            }


            /* =================================================
               ЗЕЛЁНАЯ ТОЧКА
               ================================================= */

            .online-dot {

                position: absolute;

                right: -1px;
                bottom: -1px;

                width: 18px;
                height: 18px;

                border: 3px solid #ffffff;

                border-radius: 50%;

                background: #22c55e;

                animation: onlinePulse 2s infinite;
            }


            @keyframes onlinePulse {

                0%,
                100% {

                    box-shadow:
                        0 0 0 0
                        rgba(34, 197, 94, .35);
                }

                50% {

                    box-shadow:
                        0 0 0 5px
                        rgba(34, 197, 94, 0);
                }
            }


            /* =================================================
               ВСПЛЫВАЮЩЕЕ ОКНО
               ================================================= */

            .tooltip {

                position: fixed;

                right: 24px;
                bottom: 110px;

                width: 280px;

                padding:
                    16px 18px;

                border:
                    1px solid
                    #dfe7ef;

                border-radius: 17px;

                background: #ffffff;

                color: #173653;

                font-family:
                    -apple-system,
                    BlinkMacSystemFont,
                    "Segoe UI",
                    Roboto,
                    Arial,
                    sans-serif;

                font-size: 14px;

                font-weight: 500;

                line-height: 1.5;

                box-shadow:
                    0 16px 45px
                    rgba(0, 35, 80, .20);

                opacity: 0;

                visibility: hidden;

                transform:
                    translateY(10px);

                transition:
                    opacity .2s ease,
                    transform .2s ease,
                    visibility .2s ease;

                pointer-events: none;
            }


            /* =================================================
               ХВОСТИК
               ================================================= */

            .tooltip::after {

                content: "";

                position: absolute;

                right: 25px;

                bottom: -9px;

                width: 17px;
                height: 17px;

                background: #ffffff;

                border-right:
                    1px solid
                    #dfe7ef;

                border-bottom:
                    1px solid
                    #dfe7ef;

                transform: rotate(45deg);
            }


            /* =================================================
               ПОКАЗ
               ================================================= */

            .tooltip.visible {

                opacity: 1;

                visibility: visible;

                transform:
                    translateY(0);
            }


            /* =================================================
               MOBILE
               ================================================= */

            @media (max-width: 600px) {

                .launcher {

                    right: 14px;
                    bottom: 14px;

                    width: 64px;
                    height: 64px;
                }


                .launcher img {

                    width: 53px;
                    height: 53px;
                }


                .tooltip {

                    right: 14px;
                    bottom: 98px;

                    width:
                        calc(100vw - 28px);

                    max-width: 280px;

                    font-size: 13px;
                }

            }

        </style>


        <!-- =====================================================
             КРУГЛАЯ КНОПКА
             ===================================================== -->

        <a
            id="launcherButton"
            class="launcher"
            href="${WELCOME_URL}"
            target="_top"
            aria-label="Открыть Эрудита"
        >

            <img
                src="${AVATAR_URL}"
                alt="Эрудит"
            >

            <span
                class="online-dot"
            ></span>

        </a>


        <!-- =====================================================
             ВСПЛЫВАЮЩЕЕ ОКНО
             ===================================================== -->

        <div
            id="eruditTooltip"
            class="tooltip"
        >
            Привет! Я Эрудит.<br>
            Чем могу помочь?
        </div>

    `;


    // =========================================================
    // ПОЛУЧАЕМ ЭЛЕМЕНТЫ
    // =========================================================

    const launcherButton =
        shadow.getElementById(
            "launcherButton"
        );


    const tooltip =
        shadow.getElementById(
            "eruditTooltip"
        );


    // =========================================================
    // НАВЕДЕНИЕ
    // =========================================================

    launcherButton.addEventListener(
        "mouseenter",
        function () {

            tooltip.classList.add(
                "visible"
            );

            console.log(
                "👋 Наведение на Эрудита"
            );
        }
    );


    // =========================================================
    // УХОД КУРСОРА
    // =========================================================

    launcherButton.addEventListener(
        "mouseleave",
        function () {

            tooltip.classList.remove(
                "visible"
            );
        }
    );


    // =========================================================
    // ГОТОВО
    // =========================================================

    console.log(
        "✅ Виджет Эрудита загружен"
    );

})();